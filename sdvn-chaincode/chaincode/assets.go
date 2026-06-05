/*
SPDX-License-Identifier: Apache-2.0

assets.go defines the world-state assets for the SMDAC framework
(Section 3.3.5, Figs 3.13/3.14/3.17 of the project paper).

The ledger holds the Identity Ledger (L_BC), Trust Scores, Controller keys,
Message-hash records, Flow-rule records and audit/incident logs. Each struct
carries a DocType so that CouchDB rich queries can diff identity sets
(Eq 3.23 DetectPhantomIdentities, Eq 3.57 CrossVerifyFlowRule).

Struct fields are kept in a fixed order with explicit JSON tags so marshalling
is deterministic across every endorsing peer.
*/

package chaincode

// DocType constants used as the `docType` discriminator for CouchDB rich queries.
const (
	DocTypeVehicle    = "vehicle"
	DocTypeTrust      = "trust"
	DocTypeCtrlKey    = "ctrlkey"
	DocTypeMsgHash    = "msghash"
	DocTypeFlowRule   = "flowrule"
	DocTypeAudit      = "audit"
	DocTypeIncident   = "incident"
	DocTypeCCSig      = "ccsig"
)

// Key prefixes keep the different asset families in disjoint key-spaces so a
// range scan over one prefix never returns assets of another type.
const (
	prefixVehicle  = "vehicle_"
	prefixTrust    = "trust_"
	prefixCtrlKey  = "ctrlkey_"
	prefixMsgHash  = "msghash_"
	prefixFlowRule = "flowrule_"
	prefixAudit    = "audit_"
	prefixIncident = "incident_"
	prefixCCSig    = "ccsig_"
)

// VehicleIdentity is the on-chain identity record `pkD_i` stored in L_BC by the
// direct, controller-independent registration transaction (Eq 3.55, tx_reg).
type VehicleIdentity struct {
	DocType     string `json:"docType"`     // "vehicle"
	ID          string `json:"id"`          // pseudonym ID_i
	DilithiumPK []byte `json:"dilithiumPK"` // pkD_i (ML-DSA public key)
	KyberPK     []byte `json:"kyberPK"`     // pk^Kyber_i (ML-KEM public key)
	TReg        int64  `json:"tReg"`        // registration timestamp t_reg
	RegSig      []byte `json:"regSig"`      // Dilithium.Sign(skD_i, pkD_i || t_reg)
	Revoked     bool   `json:"revoked"`     // set by RevokeVehicle (Algo 3, tx_rev)
}

// TrustScore is the per-vehicle EMA trust value T(v_i) (Eq 3.52).
type TrustScore struct {
	DocType string  `json:"docType"` // "trust"
	ID      string  `json:"id"`      // v_i
	Score   float64 `json:"score"`   // T(v_i)
	Updated int64   `json:"updated"` // last update timestamp
}

// ControllerKey holds the registered controller key `pk^{L_BC}_ctrl` plus the
// northbound-API statistical baseline (mu_NB, sigma_NB) (Eq 3.24/3.51).
type ControllerKey struct {
	DocType string  `json:"docType"` // "ctrlkey"
	CtrlID  string  `json:"ctrlId"`
	PKLBC   []byte  `json:"pkLBC"`   // pk^{L_BC}_ctrl (registered Dilithium key)
	MuNB    float64 `json:"muNB"`    // northbound baseline mean
	SigmaNB float64 `json:"sigmaNB"` // northbound baseline std-dev
}

// MessageHashRecord is the direct V2BC message-hash submission `CID^m_i`
// (Eq 3.56). It is the ground truth that VerifyMessageIntegrity (Eq 3.18)
// compares the controller-forwarded message against.
type MessageHashRecord struct {
	DocType string `json:"docType"` // "msghash"
	ID      string `json:"id"`      // v_i
	Ts      int64  `json:"ts"`      // submission timestamp
	CIDm    string `json:"cidm"`    // CID^m_i (IPFS content identifier)
	Hash    string `json:"hash"`    // SHA3-256(m_i) (hex)
	Sig     []byte `json:"sig"`     // sigma_i (Dilithium signature over m_i)
}

// FlowRuleRecord is an endorsed flow-table entry used for cross-verification
// (Eq 3.57, XV). Installed rules absent from this set indicate CC-DIM.
type FlowRuleRecord struct {
	DocType string `json:"docType"` // "flowrule"
	ID      string `json:"id"`      // v_i
	Rule    string `json:"rule"`    // endorsed flow-table entry
	Epoch   int64  `json:"epoch"`   // flow-table epoch
}

// AuditLog records `{v_i, t, CID_i, SHA3-256(L_i)}` for detection / DRL-action
// logs (Eq 3.53/3.54). The on-chain hash lets any peer verify the IPFS content.
type AuditLog struct {
	DocType string `json:"docType"` // "audit"
	ID      string `json:"id"`      // v_i
	T       int64  `json:"t"`       // event time
	CID     string `json:"cid"`     // IPFS CID of L_i
	Hash    string `json:"hash"`    // SHA3-256(L_i) (hex)
}

// IncidentLog is the immutable record of a DRL mitigation action
// (Algorithm 5, actions a6/a7/a8 and rekey logging Eq 3.40) plus optional CC
// impact metrics.
type IncidentLog struct {
	DocType string  `json:"docType"` // "incident"
	T       int64   `json:"t"`       // action time
	Action  string  `json:"action"`  // a6 / a7 / a8 / rekey
	CID     string  `json:"cid"`     // IPFS CID of incident log
	Hash    string  `json:"hash"`    // SHA3-256(L_incident) (hex)
	NER     float64 `json:"ner"`     // CC impact metrics (optional)
	DER     float64 `json:"der"`
	PFER    float64 `json:"pfer"`
	RFR     float64 `json:"rfr"`
}

// CCSignatureRecord persists the aggregated controller-compromise scores
// S^ctrl_CC-{TA,EA,SI,DIM} computed off-chain from IPFS logs (Eq 3.22-3.25),
// read by EvaluateControllerAC (Eq 3.51).
type CCSignatureRecord struct {
	DocType  string  `json:"docType"` // "ccsig"
	CtrlID   string  `json:"ctrlId"`
	T        int64   `json:"t"`
	SccTA    float64 `json:"sccTA"`  // S^ctrl_CC-TA (Eq 3.22)
	SccEA    float64 `json:"sccEA"`  // S^ctrl_CC-EA (Eq 3.25)
	SccSI    float64 `json:"sccSI"`  // S^ctrl_CC-SI (Eq 3.23)
	SccDIM   float64 `json:"sccDIM"` // S^ctrl_CC-DIM (Eq 3.24, 0 or 1)
	SccTotal float64 `json:"sccTotal"`
}
