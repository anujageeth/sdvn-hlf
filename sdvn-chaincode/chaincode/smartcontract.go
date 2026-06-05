/*
SPDX-License-Identifier: Apache-2.0

smartcontract.go implements the SMDAC Shared-Trust-Substrate chaincode
(Section 3.3.5 of the project paper). It realises the application-level layer of
the two-layer endorsement model: Fabric's native k-of-n endorsement policy
provides the ">= k independent peers" guarantee of Eq 3.49, while the
per-call Dilithium verification below provides per-vehicle/controller
non-repudiation (Eq 3.41/3.42/3.50/3.51/3.55).

Every function here is deterministic: it only verifies signatures, hashes,
reads/writes world state and compares values. All non-deterministic work
(IPFS add/pin, Kyber key-gen/enc/dec, Kyber-LKH re-keying, LLM/DRL inference,
computing the CC anomaly scores from raw logs) is performed off-chain by the
application plane, which then submits the resulting CIDs / hashes / scores to
the functions below.
*/

package chaincode

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/hyperledger/fabric-contract-api-go/v2/contractapi"
)

// SmartContract provides the SMDAC chaincode functions.
type SmartContract struct {
	contractapi.Contract
}

// neutralTrust is the starting trust value assigned at registration.
const neutralTrust = 1.0

// InitLedger is a no-op kept for deployment compatibility (the SMDAC ledger is
// populated by RegisterVehicle / RegisterControllerKey, not by seeding).
func (s *SmartContract) InitLedger(ctx contractapi.TransactionContextInterface) error {
	return nil
}

// =====================================================================================
// Vehicle identity & trust (Eq 3.50, 3.52, 3.55; Algo 3)
// =====================================================================================

// RegisterVehicle realises tx_reg (Eq 3.55). It is a direct V2BC operation that
// bypasses the controller: it verifies the Dilithium signature over
// (pkD_i || t_reg), stores the VehicleIdentity in L_BC and initialises the
// vehicle's trust score to a neutral value.
func (s *SmartContract) RegisterVehicle(ctx contractapi.TransactionContextInterface,
	id string, dilithiumPK []byte, kyberPK []byte, tReg int64, regSig []byte) error {

	if id == "" {
		return fmt.Errorf("vehicle id must not be empty")
	}
	exists, err := s.entityExists(ctx, prefixVehicle+id)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("vehicle %s already registered", id)
	}

	// Verify Dilithium signature over (pkD_i || t_reg) — proves key ownership.
	msg := append(append([]byte{}, dilithiumPK...), []byte(strconv.FormatInt(tReg, 10))...)
	if !DilithiumVerify(dilithiumPK, msg, regSig) {
		return fmt.Errorf("invalid registration signature for vehicle %s", id)
	}

	v := VehicleIdentity{
		DocType:     DocTypeVehicle,
		ID:          id,
		DilithiumPK: dilithiumPK,
		KyberPK:     kyberPK,
		TReg:        tReg,
		RegSig:      regSig,
		Revoked:     false,
	}
	if err := putJSON(ctx, prefixVehicle+id, v); err != nil {
		return err
	}

	t := TrustScore{DocType: DocTypeTrust, ID: id, Score: neutralTrust, Updated: tReg}
	return putJSON(ctx, prefixTrust+id, t)
}

// ReadVehicle returns the VehicleIdentity for id, or an error if absent.
func (s *SmartContract) ReadVehicle(ctx contractapi.TransactionContextInterface, id string) (*VehicleIdentity, error) {
	var v VehicleIdentity
	ok, err := getJSON(ctx, prefixVehicle+id, &v)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("vehicle %s does not exist", id)
	}
	return &v, nil
}

// VehicleExists reports whether v_i is registered in L_BC.
func (s *SmartContract) VehicleExists(ctx contractapi.TransactionContextInterface, id string) (bool, error) {
	return s.entityExists(ctx, prefixVehicle+id)
}

// EvaluateVehicleAC realises AC(v_i, op) (Eq 3.50):
//
//	AC = Dilithium.Verify(pkD_i, m_i, sigma_i) AND T(v_i) >= tau_min AND v_i in L_BC
//
// It returns false (not an error) when the vehicle is unknown/revoked or any
// condition fails, so callers can branch directly on the boolean result.
func (s *SmartContract) EvaluateVehicleAC(ctx contractapi.TransactionContextInterface,
	id string, msg []byte, sig []byte, tauMin float64) (bool, error) {

	var v VehicleIdentity
	ok, err := getJSON(ctx, prefixVehicle+id, &v)
	if err != nil {
		return false, err
	}
	if !ok || v.Revoked { // v_i not in L_BC, or revoked
		return false, nil
	}
	if !DilithiumVerify(v.DilithiumPK, msg, sig) {
		return false, nil
	}

	var t TrustScore
	ok, err = getJSON(ctx, prefixTrust+id, &t)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return t.Score >= tauMin, nil
}

// UpdateTrustScore realises the EMA trust update (Eq 3.52):
//
//	T^{t+1} = lambda*T^t + (1-lambda)*1[ S_comp < theta_adapt ]
//
// belowThreshold carries the indicator 1[S_comp < theta_adapt]; passing true
// keeps trust high (benign), false degrades it (detection).
func (s *SmartContract) UpdateTrustScore(ctx contractapi.TransactionContextInterface,
	id string, lambda float64, belowThreshold bool, ts int64) error {

	var t TrustScore
	ok, err := getJSON(ctx, prefixTrust+id, &t)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("trust score for vehicle %s does not exist", id)
	}

	ind := 0.0
	if belowThreshold {
		ind = 1.0
	}
	t.Score = lambda*t.Score + (1-lambda)*ind
	t.Updated = ts
	return putJSON(ctx, prefixTrust+id, t)
}

// GetTrustScore returns the current trust record for a vehicle.
func (s *SmartContract) GetTrustScore(ctx contractapi.TransactionContextInterface, id string) (*TrustScore, error) {
	var t TrustScore
	ok, err := getJSON(ctx, prefixTrust+id, &t)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("trust score for vehicle %s does not exist", id)
	}
	return &t, nil
}

// RevokeVehicle realises tx_rev (Algorithm 3). It marks the identity revoked and
// drives the trust score to zero, recording both on-chain.
func (s *SmartContract) RevokeVehicle(ctx contractapi.TransactionContextInterface, id string, ts int64) error {
	v, err := s.ReadVehicle(ctx, id)
	if err != nil {
		return err
	}
	v.Revoked = true
	if err := putJSON(ctx, prefixVehicle+id, *v); err != nil {
		return err
	}

	var t TrustScore
	ok, err := getJSON(ctx, prefixTrust+id, &t)
	if err != nil {
		return err
	}
	if !ok {
		t = TrustScore{DocType: DocTypeTrust, ID: id}
	}
	t.Score = 0
	t.Updated = ts
	return putJSON(ctx, prefixTrust+id, t)
}

// =====================================================================================
// Message-integrity (Eq 3.18, 3.56) and audit logging (Eq 3.53/3.54)
// =====================================================================================

// SubmitMessageHash realises CID^m_i (Eq 3.56). This is a direct V2BC operation
// that bypasses the controller, establishing the tamper-evident ground truth
// {SHA3-256(m_i), sigma_i, ts, CID} against which VerifyMessageIntegrity checks.
func (s *SmartContract) SubmitMessageHash(ctx contractapi.TransactionContextInterface,
	id string, ts int64, cidM string, hash string, sig []byte) error {

	rec := MessageHashRecord{
		DocType: DocTypeMsgHash,
		ID:      id,
		Ts:      ts,
		CIDm:    cidM,
		Hash:    hash,
		Sig:     sig,
	}
	key, err := ctx.GetStub().CreateCompositeKey(DocTypeMsgHash, []string{id, strconv.FormatInt(ts, 10)})
	if err != nil {
		return err
	}
	return putJSON(ctx, key, rec)
}

// VerifyMessageIntegrity realises the DIM hash compare (Eq 3.18). It returns the
// per-message integrity indicator S^{(i)}_DIM = 1[ forwardedHash != on-chain ].
// A return of true means the controller-forwarded message was tampered with.
func (s *SmartContract) VerifyMessageIntegrity(ctx contractapi.TransactionContextInterface,
	id string, ts int64, forwardedHash string) (bool, error) {

	key, err := ctx.GetStub().CreateCompositeKey(DocTypeMsgHash, []string{id, strconv.FormatInt(ts, 10)})
	if err != nil {
		return false, err
	}
	var rec MessageHashRecord
	ok, err := getJSON(ctx, key, &rec)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("no on-chain message hash for vehicle %s at ts %d", id, ts)
	}
	// S^{(i)}_DIM = 1 iff mismatch.
	return rec.Hash != forwardedHash, nil
}

// WriteAuditLog realises the IPFS+chain audit write (Eq 3.53/3.54). The off-chain
// app pins L_i to >= n_pin IPFS nodes and passes the returned CID + SHA3-256(L_i)
// here; the on-chain hash lets any peer later verify the IPFS content.
func (s *SmartContract) WriteAuditLog(ctx contractapi.TransactionContextInterface,
	id string, t int64, cid string, hash string) error {

	rec := AuditLog{DocType: DocTypeAudit, ID: id, T: t, CID: cid, Hash: hash}
	key, err := ctx.GetStub().CreateCompositeKey(DocTypeAudit, []string{id, strconv.FormatInt(t, 10)})
	if err != nil {
		return err
	}
	return putJSON(ctx, key, rec)
}

// =====================================================================================
// Flow-rule cross-verification (Eq 3.57) and phantom-identity detection (Eq 3.23)
// =====================================================================================

// RegisterFlowRule stores an endorsed flow-table entry (a FlowRuleRecord) that
// CrossVerifyFlowRule later checks installed rules against.
func (s *SmartContract) RegisterFlowRule(ctx contractapi.TransactionContextInterface,
	id string, rule string, epoch int64) error {

	rec := FlowRuleRecord{DocType: DocTypeFlowRule, ID: id, Rule: rule, Epoch: epoch}
	key, err := ctx.GetStub().CreateCompositeKey(DocTypeFlowRule, []string{id, strconv.FormatInt(epoch, 10)})
	if err != nil {
		return err
	}
	return putJSON(ctx, key, rec)
}

// CrossVerifyFlowRule realises XV (Eq 3.57): XV = 1 iff the installed flow rule
// for v_i matches one of the endorsed FlowRuleRecords on-chain. XV = 0 is a
// CC-DIM indicator (controller installed a rule absent from the ledger).
func (s *SmartContract) CrossVerifyFlowRule(ctx contractapi.TransactionContextInterface,
	id string, installedRule string) (bool, error) {

	iter, err := ctx.GetStub().GetStateByPartialCompositeKey(DocTypeFlowRule, []string{id})
	if err != nil {
		return false, err
	}
	defer iter.Close()

	for iter.HasNext() {
		kv, err := iter.Next()
		if err != nil {
			return false, err
		}
		var rec FlowRuleRecord
		if err := json.Unmarshal(kv.Value, &rec); err != nil {
			return false, err
		}
		if rec.Rule == installedRule {
			return true, nil // XV = 1
		}
	}
	return false, nil // XV = 0 -> CC-DIM indicator
}

// DetectPhantomIdentities realises S^ctrl_CC-SI (Eq 3.23): it counts/returns the
// flow-table identities that are absent from the blockchain ledger L_BC. The
// caller passes the controller's flow-table identity set as a JSON string array
// (F_table); each id not registered (or revoked) is a phantom identity.
func (s *SmartContract) DetectPhantomIdentities(ctx contractapi.TransactionContextInterface,
	flowTableIDsJSON string) ([]string, error) {

	var ids []string
	if err := json.Unmarshal([]byte(flowTableIDsJSON), &ids); err != nil {
		return nil, fmt.Errorf("flowTableIDs must be a JSON array of strings: %v", err)
	}

	phantoms := make([]string, 0)
	for _, id := range ids {
		var v VehicleIdentity
		ok, err := getJSON(ctx, prefixVehicle+id, &v)
		if err != nil {
			return nil, err
		}
		if !ok || v.Revoked { // not in L_BC (or no longer valid) => phantom
			phantoms = append(phantoms, id)
		}
	}
	return phantoms, nil
}

// =====================================================================================
// Controller key & legitimacy (Eq 3.24, 3.51) and CC-signature persistence (Eq 3.22-3.25)
// =====================================================================================

// RegisterControllerKey stores pk^{L_BC}_ctrl plus the northbound-API baseline
// (mu_NB, sigma_NB) (Eq 3.24/3.51).
func (s *SmartContract) RegisterControllerKey(ctx contractapi.TransactionContextInterface,
	ctrlID string, pkLBC []byte, muNB float64, sigmaNB float64) error {

	ck := ControllerKey{DocType: DocTypeCtrlKey, CtrlID: ctrlID, PKLBC: pkLBC, MuNB: muNB, SigmaNB: sigmaNB}
	return putJSON(ctx, prefixCtrlKey+ctrlID, ck)
}

// GetControllerKey returns the registered controller key record.
func (s *SmartContract) GetControllerKey(ctx contractapi.TransactionContextInterface, ctrlID string) (*ControllerKey, error) {
	var ck ControllerKey
	ok, err := getJSON(ctx, prefixCtrlKey+ctrlID, &ck)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("controller key %s does not exist", ctrlID)
	}
	return &ck, nil
}

// RecordCCSignatures persists the aggregated controller-compromise scores
// S^ctrl_CC-{TA,EA,SI,DIM} (Eq 3.22-3.25) that the application computed off-chain
// from the IPFS logs, for use by EvaluateControllerAC.
func (s *SmartContract) RecordCCSignatures(ctx contractapi.TransactionContextInterface,
	ctrlID string, t int64, sccTA float64, sccEA float64, sccSI float64, sccDIM float64) error {

	rec := CCSignatureRecord{
		DocType:  DocTypeCCSig,
		CtrlID:   ctrlID,
		T:        t,
		SccTA:    sccTA,
		SccEA:    sccEA,
		SccSI:    sccSI,
		SccDIM:   sccDIM,
		SccTotal: sccTA + sccEA + sccSI + sccDIM,
	}
	return putJSON(ctx, prefixCCSig+ctrlID, rec)
}

// EvaluateControllerAC realises AC(ctrl, op) (Eq 3.51):
//
//	AC = S^ctrl_CC < theta_CC AND pk_used = pk^{L_BC}_ctrl AND |E_NB| < mu_NB + 3 sigma_NB
//
// pkUsed is the Dilithium key the controller currently signs with; eNB is the
// observed northbound-API call count in the window. The aggregated S^ctrl_CC and
// (mu_NB, sigma_NB) baseline are read from the on-chain records, never from the
// (possibly compromised) controller.
func (s *SmartContract) EvaluateControllerAC(ctx contractapi.TransactionContextInterface,
	ctrlID string, pkUsed []byte, eNB float64, thetaCC float64) (bool, error) {

	var ck ControllerKey
	ok, err := getJSON(ctx, prefixCtrlKey+ctrlID, &ck)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("controller key %s does not exist", ctrlID)
	}

	var cc CCSignatureRecord
	ok, err = getJSON(ctx, prefixCCSig+ctrlID, &cc)
	if err != nil {
		return false, err
	}
	if !ok {
		// No anomaly recorded yet => treat aggregate score as 0 (legitimate).
		cc = CCSignatureRecord{}
	}

	// Condition 1: S^ctrl_CC < theta_CC.
	if cc.SccTotal >= thetaCC {
		return false, nil
	}
	// Condition 2: pk_used = pk^{L_BC}_ctrl (Eq 3.24 key-mismatch check).
	if !bytesEqual(pkUsed, ck.PKLBC) {
		return false, nil
	}
	// Condition 3: |E_NB| < mu_NB + 3 sigma_NB.
	if eNB >= ck.MuNB+3*ck.SigmaNB {
		return false, nil
	}
	return true, nil
}

// =====================================================================================
// DRL mitigation actions (Algorithm 5: a6/a7/a8; Algorithm 1 rekey)
// =====================================================================================

// ActivateStandbyController realises tx_fail (Algorithm 5, action a6): the
// endorsed standby-controller handoff. The new controller key is registered and
// the event is recorded as an incident.
func (s *SmartContract) ActivateStandbyController(ctx contractapi.TransactionContextInterface,
	standbyCtrlID string, standbyPK []byte, muNB float64, sigmaNB float64, t int64, cid string, hash string) error {

	if err := s.RegisterControllerKey(ctx, standbyCtrlID, standbyPK, muNB, sigmaNB); err != nil {
		return err
	}
	return s.LogIncident(ctx, t, "a6", cid, hash, 0, 0, 0, 0)
}

// ReRegisterVehicles realises tx_rereg (Algorithm 5, action a6): re-register the
// given blockchain-verified vehicles to the standby controller. Only ids already
// present and non-revoked in L_BC are accepted; the returned slice lists them.
func (s *SmartContract) ReRegisterVehicles(ctx contractapi.TransactionContextInterface,
	vehicleIDsJSON string, t int64) ([]string, error) {

	var ids []string
	if err := json.Unmarshal([]byte(vehicleIDsJSON), &ids); err != nil {
		return nil, fmt.Errorf("vehicleIDs must be a JSON array of strings: %v", err)
	}

	reregistered := make([]string, 0, len(ids))
	for _, id := range ids {
		var v VehicleIdentity
		ok, err := getJSON(ctx, prefixVehicle+id, &v)
		if err != nil {
			return nil, err
		}
		if !ok || v.Revoked {
			continue // only ledger-verified vehicles are re-registered
		}
		reregistered = append(reregistered, id)
	}
	if err := s.LogIncident(ctx, t, "tx_rereg", "", "", 0, 0, 0, 0); err != nil {
		return nil, err
	}
	return reregistered, nil
}

// FlowTablePurge realises tx_purge (Algorithm 5, action a7): replace the flow
// table with ledger-endorsed entries only. The caller passes its installed
// flow-table entries as a JSON string array; this returns the subset that is
// endorsed on-chain (the purged-down table).
func (s *SmartContract) FlowTablePurge(ctx contractapi.TransactionContextInterface,
	installedRulesJSON string, t int64) ([]string, error) {

	var rules []FlowRuleRecord
	if err := json.Unmarshal([]byte(installedRulesJSON), &rules); err != nil {
		return nil, fmt.Errorf("installedRules must be a JSON array of {id,rule}: %v", err)
	}

	kept := make([]string, 0, len(rules))
	for _, r := range rules {
		ok, err := s.CrossVerifyFlowRule(ctx, r.ID, r.Rule)
		if err != nil {
			return nil, err
		}
		if ok {
			kept = append(kept, r.Rule)
		}
	}
	if err := s.LogIncident(ctx, t, "a7", "", "", 0, 0, 0, 0); err != nil {
		return nil, err
	}
	return kept, nil
}

// LogIncident / RecordDRLAction realise the immutable DRL-action record
// (Algorithm 1 & 5, including a8 API lockdown and Eq 3.40 rekey logging).
func (s *SmartContract) LogIncident(ctx contractapi.TransactionContextInterface,
	t int64, action string, cid string, hash string, ner float64, der float64, pfer float64, rfr float64) error {

	rec := IncidentLog{
		DocType: DocTypeIncident,
		T:       t,
		Action:  action,
		CID:     cid,
		Hash:    hash,
		NER:     ner,
		DER:     der,
		PFER:    pfer,
		RFR:     rfr,
	}
	key, err := ctx.GetStub().CreateCompositeKey(DocTypeIncident, []string{strconv.FormatInt(t, 10), action})
	if err != nil {
		return err
	}
	return putJSON(ctx, key, rec)
}

// RecordRekey logs an Algorithm-1 PQC re-key event: only the new group-key hash
// and the IPFS CID are stored on-chain (Eq 3.40, Algo 1 lines 13-15).
func (s *SmartContract) RecordRekey(ctx contractapi.TransactionContextInterface,
	t int64, groupKeyHash string, cid string) error {
	return s.LogIncident(ctx, t, "rekey", cid, groupKeyHash, 0, 0, 0, 0)
}

// =====================================================================================
// Read-only helpers
// =====================================================================================

// GetAllVehicles returns every VehicleIdentity in L_BC (range scan over the
// vehicle_ key-space; works on both LevelDB and CouchDB state databases).
func (s *SmartContract) GetAllVehicles(ctx contractapi.TransactionContextInterface) ([]*VehicleIdentity, error) {
	iter, err := ctx.GetStub().GetStateByRange(prefixVehicle, prefixRangeEnd(prefixVehicle))
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	vehicles := make([]*VehicleIdentity, 0)
	for iter.HasNext() {
		kv, err := iter.Next()
		if err != nil {
			return nil, err
		}
		var v VehicleIdentity
		if err := json.Unmarshal(kv.Value, &v); err != nil {
			return nil, err
		}
		vehicles = append(vehicles, &v)
	}
	return vehicles, nil
}

// GetMessageHistory returns all on-chain message-hash records for a vehicle.
func (s *SmartContract) GetMessageHistory(ctx contractapi.TransactionContextInterface, id string) ([]*MessageHashRecord, error) {
	iter, err := ctx.GetStub().GetStateByPartialCompositeKey(DocTypeMsgHash, []string{id})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	recs := make([]*MessageHashRecord, 0)
	for iter.HasNext() {
		kv, err := iter.Next()
		if err != nil {
			return nil, err
		}
		var r MessageHashRecord
		if err := json.Unmarshal(kv.Value, &r); err != nil {
			return nil, err
		}
		recs = append(recs, &r)
	}
	return recs, nil
}

// =====================================================================================
// Internal helpers
// =====================================================================================

func (s *SmartContract) entityExists(ctx contractapi.TransactionContextInterface, key string) (bool, error) {
	b, err := ctx.GetStub().GetState(key)
	if err != nil {
		return false, fmt.Errorf("failed to read %s from world state: %v", key, err)
	}
	return b != nil, nil
}

func putJSON(ctx contractapi.TransactionContextInterface, key string, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ctx.GetStub().PutState(key, b)
}

// getJSON loads key into out. The bool reports whether the key existed.
func getJSON(ctx contractapi.TransactionContextInterface, key string, out interface{}) (bool, error) {
	b, err := ctx.GetStub().GetState(key)
	if err != nil {
		return false, fmt.Errorf("failed to read %s from world state: %v", key, err)
	}
	if b == nil {
		return false, nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return false, err
	}
	return true, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// prefixRangeEnd returns the exclusive end key for an open range over all keys
// beginning with prefix (used for LevelDB/CouchDB-agnostic prefix scans).
func prefixRangeEnd(prefix string) string {
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xff {
			b[i]++
			return string(b[:i+1])
		}
	}
	return "" // open-ended (prefix was all 0xff)
}
