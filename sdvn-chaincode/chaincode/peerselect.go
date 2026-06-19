/*
SPDX-License-Identifier: Apache-2.0

peerselect.go implements runtime trust-based endorsing-peer selection
(supervisor guidance; Eq 3.55/3.56). Instead of pinning every node as a peer —
which is computationally expensive and suboptimal — the trusted subset of nodes
is (re)selected occasionally at runtime by ranking on-chain peer trust scores
T(p_j). The selection is BFT-sized k = floor(2n/3)+1 (Eq 3.56) and is enforced
at the Fabric layer by a state-based endorsement (SBE) policy bound to the
governance keys, so only the currently trusted set can change the trusted set or
the security thresholds.

Bootstrap rule (supervisor guidance): peer trust starts at neutralTrust for every
node, so trust-ranking is meaningless at t=0. The substrate is therefore seeded
once via SeedEndorserSet — either with EVERY node ("all-seed") or with a
trusted-authority-chosen subset ("ta-seed") — and ReselectEndorsers takes over
once the scores have diverged.
*/

package chaincode

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hyperledger/fabric-contract-api-go/v2/contractapi"
	"github.com/hyperledger/fabric-protos-go-apiv2/common"
	"github.com/hyperledger/fabric-protos-go-apiv2/msp"
	"google.golang.org/protobuf/proto"
)

// =====================================================================================
// Peer (node) trust — the signal runtime selection ranks on
// =====================================================================================

// RegisterPeerNode seeds a neutral trust record for a candidate endorsing-peer
// org. nodeID is the org MSP identifier (e.g. "Org3MSP") so the score maps
// directly onto the Fabric endorsement policy. Idempotency is enforced: a second
// registration of the same node is rejected rather than silently resetting trust.
func (s *SmartContract) RegisterPeerNode(ctx contractapi.TransactionContextInterface,
	nodeID string, ts int64) error {

	if nodeID == "" {
		return fmt.Errorf("node (MSP) id must not be empty")
	}
	exists, err := s.entityExists(ctx, prefixPeerTrust+nodeID)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("peer node %s already registered", nodeID)
	}
	pt := PeerTrust{DocType: DocTypePeerTrust, NodeID: nodeID, Score: neutralTrust, Updated: ts}
	return putJSON(ctx, prefixPeerTrust+nodeID, pt)
}

// UpdatePeerTrustScore applies the EMA peer trust update (mirrors the vehicle
// rule of Eq 3.60):
//
//	T^{t+1}(p_j) = lambda*T^t(p_j) + (1-lambda)*1[ p_j well-behaved ]
//
// wellBehaved carries the per-interval indicator (e.g. the peer endorsed honestly
// and passed the chaincode-integrity check V_CC=1 of Eq 3.74): true keeps trust
// high, false degrades it so the node falls out of the next selection.
func (s *SmartContract) UpdatePeerTrustScore(ctx contractapi.TransactionContextInterface,
	nodeID string, lambda float64, wellBehaved bool, ts int64) error {

	var pt PeerTrust
	ok, err := getJSON(ctx, prefixPeerTrust+nodeID, &pt)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("peer trust for node %s does not exist", nodeID)
	}

	ind := 0.0
	if wellBehaved {
		ind = 1.0
	}
	pt.Score = lambda*pt.Score + (1-lambda)*ind
	pt.Updated = ts
	return putJSON(ctx, prefixPeerTrust+nodeID, pt)
}

// GetPeerTrustScore returns the current trust record for a peer node.
func (s *SmartContract) GetPeerTrustScore(ctx contractapi.TransactionContextInterface, nodeID string) (*PeerTrust, error) {
	var pt PeerTrust
	ok, err := getJSON(ctx, prefixPeerTrust+nodeID, &pt)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("peer trust for node %s does not exist", nodeID)
	}
	return &pt, nil
}

// GetAllPeerTrust returns every PeerTrust record (range scan over the peertrust_
// key-space). The endorser-set singleton lives outside this prefix, so it is
// never returned here.
func (s *SmartContract) GetAllPeerTrust(ctx contractapi.TransactionContextInterface) ([]*PeerTrust, error) {
	iter, err := ctx.GetStub().GetStateByRange(prefixPeerTrust, prefixRangeEnd(prefixPeerTrust))
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	out := make([]*PeerTrust, 0)
	for iter.HasNext() {
		kv, err := iter.Next()
		if err != nil {
			return nil, err
		}
		var pt PeerTrust
		if err := json.Unmarshal(kv.Value, &pt); err != nil {
			return nil, err
		}
		if pt.DocType != DocTypePeerTrust { // defensive: ignore any foreign record
			continue
		}
		out = append(out, &pt)
	}
	return out, nil
}

// =====================================================================================
// Endorser-set selection (Eq 3.55/3.56)
// =====================================================================================

// SeedEndorserSet performs the one-time bootstrap selection (supervisor guidance:
// "initially you can have everyone as peers or select peers using a trusted
// authority, as node trust scores are just initialized"). The caller passes the
// initial MSP-id set as a JSON string array; bySel records HOW it was chosen
// ("all-seed" = every node, "ta-seed" = trusted-authority subset). Any listed
// node without a trust record yet is seeded at neutralTrust. It writes the
// ActiveEndorserSet and binds the governance keys with the BFT SBE policy.
func (s *SmartContract) SeedEndorserSet(ctx contractapi.TransactionContextInterface,
	mspIDsJSON string, bySel string, epoch int64, ts int64) (*ActiveEndorserSet, error) {

	var ids []string
	if err := json.Unmarshal([]byte(mspIDsJSON), &ids); err != nil {
		return nil, fmt.Errorf("mspIDs must be a JSON array of strings: %v", err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("initial endorser set must not be empty")
	}
	if bySel != "all-seed" && bySel != "ta-seed" {
		return nil, fmt.Errorf("bySel must be \"all-seed\" or \"ta-seed\"")
	}

	for _, id := range ids {
		if id == "" {
			return nil, fmt.Errorf("MSP id must not be empty")
		}
		exists, err := s.entityExists(ctx, prefixPeerTrust+id)
		if err != nil {
			return nil, err
		}
		if !exists {
			pt := PeerTrust{DocType: DocTypePeerTrust, NodeID: id, Score: neutralTrust, Updated: ts}
			if err := putJSON(ctx, prefixPeerTrust+id, pt); err != nil {
				return nil, err
			}
		}
	}
	return s.writeEndorserSet(ctx, dedupe(ids), bySel, epoch, ts)
}

// ReselectEndorsers performs the runtime, periodic trust-based selection
// (supervisor guidance; re-run every interval Delta). It ranks all registered
// peer nodes by T(p_j) (descending, MSP-id tie-break for determinism), takes the
// top nPeers as the new trusted set P, sizes the BFT threshold k=floor(2|P|/3)+1
// (Eq 3.56) and rewrites the ActiveEndorserSet — re-binding the governance SBE
// policy to the freshly selected set. Because the endorser-set key is itself
// bound by the previous selection's SBE policy, this transaction must be endorsed
// by the currently trusted set, so no single node can hijack the selection.
func (s *SmartContract) ReselectEndorsers(ctx contractapi.TransactionContextInterface,
	nPeers int, epoch int64, ts int64) (*ActiveEndorserSet, error) {

	if nPeers < 1 {
		return nil, fmt.Errorf("nPeers must be >= 1")
	}
	all, err := s.GetAllPeerTrust(ctx)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no registered peer nodes; call RegisterPeerNode/SeedEndorserSet first")
	}

	// Deterministic rank: score desc, then NodeID asc so every endorsing peer
	// computes the identical ordering (and thus identical write-set).
	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].NodeID < all[j].NodeID
	})

	if nPeers > len(all) {
		nPeers = len(all)
	}
	ids := make([]string, 0, nPeers)
	for i := 0; i < nPeers; i++ {
		ids = append(ids, all[i].NodeID)
	}
	return s.writeEndorserSet(ctx, ids, "trust-rank", epoch, ts)
}

// GetActiveEndorserSet returns the current trust-selected endorsing-peer set so
// the application/gateway plane can route endorsement proposals only to the
// trusted peers (and an admin can refresh the committed chaincode EP to match).
func (s *SmartContract) GetActiveEndorserSet(ctx contractapi.TransactionContextInterface) (*ActiveEndorserSet, error) {
	var set ActiveEndorserSet
	ok, err := getJSON(ctx, singletonEndorserSet, &set)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no active endorser set; call SeedEndorserSet first")
	}
	return &set, nil
}

// =====================================================================================
// Internal helpers
// =====================================================================================

// writeEndorserSet persists the ActiveEndorserSet record and (re)binds the
// governance keys with the BFT state-based endorsement policy over the selected
// set. Shared by SeedEndorserSet and ReselectEndorsers.
func (s *SmartContract) writeEndorserSet(ctx contractapi.TransactionContextInterface,
	ids []string, bySel string, epoch int64, ts int64) (*ActiveEndorserSet, error) {

	n := len(ids)
	k := bftThreshold(n)
	set := ActiveEndorserSet{
		DocType: DocTypeEndorserSet,
		MSPIDs:  ids,
		K:       k,
		N:       n,
		Epoch:   epoch,
		BySel:   bySel,
		Updated: ts,
	}
	if err := putJSON(ctx, singletonEndorserSet, set); err != nil {
		return nil, err
	}
	if err := s.applyGovernanceSBE(ctx, ids, k); err != nil {
		return nil, err
	}
	return &set, nil
}

// applyGovernanceSBE binds the endorser-set and SystemConfig singletons with a
// k-of-n state-based endorsement policy over the selected MSPs, so changing the
// trusted set or the security thresholds requires a BFT quorum of the CURRENTLY
// trusted peers (and nothing else).
func (s *SmartContract) applyGovernanceSBE(ctx contractapi.TransactionContextInterface,
	mspIDs []string, k int) error {

	ep, err := buildThresholdEP(mspIDs, k)
	if err != nil {
		return err
	}
	for _, key := range []string{singletonEndorserSet, singletonSysConfig} {
		if err := ctx.GetStub().SetStateValidationParameter(key, ep); err != nil {
			return fmt.Errorf("failed to set SBE policy on %s: %w", key, err)
		}
	}
	return nil
}

// bftThreshold returns the supermajority endorsement threshold k = floor(2n/3)+1
// of Eq 3.56 (tolerating f = floor((n-1)/3) Byzantine peers).
func bftThreshold(n int) int {
	return (2*n)/3 + 1
}

// buildThresholdEP constructs a marshalled k-of-n SignaturePolicyEnvelope
// requiring PEER-role signatures from the given MSPs. This is the exact wire
// format consumed by SetStateValidationParameter (state-based endorsement).
func buildThresholdEP(mspIDs []string, k int) ([]byte, error) {
	n := len(mspIDs)
	if n == 0 {
		return nil, fmt.Errorf("endorser set must not be empty")
	}
	if k < 1 || k > n {
		return nil, fmt.Errorf("threshold k=%d out of range for n=%d", k, n)
	}

	identities := make([]*msp.MSPPrincipal, n)
	rules := make([]*common.SignaturePolicy, n)
	for i, id := range mspIDs {
		roleBytes, err := proto.Marshal(&msp.MSPRole{
			Role:          msp.MSPRole_PEER,
			MspIdentifier: id,
		})
		if err != nil {
			return nil, err
		}
		identities[i] = &msp.MSPPrincipal{
			PrincipalClassification: msp.MSPPrincipal_ROLE,
			Principal:               roleBytes,
		}
		rules[i] = &common.SignaturePolicy{
			Type: &common.SignaturePolicy_SignedBy{SignedBy: int32(i)},
		}
	}

	env := &common.SignaturePolicyEnvelope{
		Version: 0,
		Rule: &common.SignaturePolicy{
			Type: &common.SignaturePolicy_NOutOf_{
				NOutOf: &common.SignaturePolicy_NOutOf{
					N:     int32(k),
					Rules: rules,
				},
			},
		},
		Identities: identities,
	}
	return proto.Marshal(env)
}

// dedupe removes duplicate MSP ids while preserving first-seen order, so a seed
// list never produces a malformed (repeated-principal) endorsement policy.
func dedupe(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
