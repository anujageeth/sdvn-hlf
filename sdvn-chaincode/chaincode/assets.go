/*
SPDX-License-Identifier: Apache-2.0

assets.go defines the world-state assets for the SMDAC framework
(Section 3.3.5, Figs 3.13/3.14/3.17 of the project paper, Eqs 3.55-3.74).

The ledger holds the Identity Ledger (L_BC), vehicle/controller trust scores,
controller keys, message-hash records, flow-rule records, audit/incident logs,
IPFS-availability records and the committed chaincode-integrity hash. Each struct
carries a DocType so that CouchDB rich queries can diff identity sets
(Eq 3.23 DetectPhantomIdentities, Eq 3.72 CrossVerifyFlowRule).

Struct fields are kept in a fixed order with explicit JSON tags so marshalling
is deterministic across every endorsing peer.
*/

package chaincode

// DocType constants used as the `docType` discriminator for CouchDB rich queries.
const (
	DocTypeVehicle    = "vehicle"
	DocTypeTrust      = "trust"
	DocTypeCtrlKey    = "ctrlkey"
	DocTypeCtrlTrust  = "ctrltrust"
	DocTypeMsgHash    = "msghash"
	DocTypeFlowRule   = "flowrule"
	DocTypeAudit      = "audit"
	DocTypeIncident   = "incident"
	DocTypeCCSig      = "ccsig"
	DocTypeIPFSAvail  = "ipfsavail"
	DocTypeCChash     = "cchash"
)

// Key prefixes keep the different asset families in disjoint key-spaces so a
// range scan over one prefix never returns assets of another type.
const (
	prefixVehicle   = "vehicle_"
	prefixTrust     = "trust_"
	prefixCtrlKey   = "ctrlkey_"
	prefixCtrlTrust = "ctrltrust_"
	prefixMsgHash   = "msghash_"
	prefixFlowRule  = "flowrule_"
	prefixAudit     = "audit_"
	prefixIncident  = "incident_"
	prefixCCSig     = "ccsig_"
	prefixIPFSAvail = "ipfsavail_"
	prefixCChash    = "cchash_"
)

// singletonIPFSAvail keys the most-recent IPFS-availability record so that
// EvaluateConservativeFailover (Eq 3.69) can read Q_IPFS(t) without scanning.
const singletonIPFSAvail = prefixIPFSAvail + "latest"

// singletonCChash keys the (single) committed chaincode-integrity hash H_CC
// (Eq 3.73). There is exactly one deployed chaincode per channel.
const singletonCChash = prefixCChash + "current"

// VehicleIdentity is the on-chain identity record `pkD_i` stored in L_BC by the
// direct, controller-independent registration transaction (Eq 3.70, tx_reg).
type VehicleIdentity struct {
	DocType     string `json:"docType"`     // "vehicle"
	ID          string `json:"id"`          // pseudonym ID_i
	DilithiumPK []byte `json:"dilithiumPK"` // pkD_i (ML-DSA public key)
	KyberPK     []byte `json:"kyberPK"`     // pk^Kyber_i (ML-KEM public key)
	TReg        int64  `json:"tReg"`        // registration timestamp t_reg
	RegSig      []byte `json:"regSig"`      // Dilithium.Sign(skD_i, pkD_i || t_reg)
	Revoked     bool   `json:"revoked"`     // set by RevokeVehicle (Algo 4/5, tx_rev)
}

// TrustScore is the per-vehicle EMA trust value T(v_i) (Eq 3.60).
type TrustScore struct {
	DocType string  `json:"docType"` // "trust"
	ID      string  `json:"id"`      // v_i
	Score   float64 `json:"score"`   // T(v_i)
	Updated int64   `json:"updated"` // last update timestamp
}

// ControllerKey holds the registered controller key `pk^{L_BC}_ctrl` plus the
// northbound-API statistical baseline (mu_NB, sigma_NB) (Eq 3.24/3.59).
type ControllerKey struct {
	DocType string  `json:"docType"` // "ctrlkey"
	CtrlID  string  `json:"ctrlId"`
	PKLBC   []byte  `json:"pkLBC"`   // pk^{L_BC}_ctrl (registered Dilithium key)
	MuNB    float64 `json:"muNB"`    // northbound baseline mean
	SigmaNB float64 `json:"sigmaNB"` // northbound baseline std-dev
}

// ControllerTrust is the per-controller EMA trust value T(ctrl) (Eq 3.61) plus
// the isolation state used by the standby/re-admission guards (Eq 3.63/3.64).
// It is re-evaluated every interval Delta from L_BC / N_pin only (Eq 3.62),
// never from controller-reported state.
type ControllerTrust struct {
	DocType     string  `json:"docType"`     // "ctrltrust"
	CtrlID      string  `json:"ctrlId"`
	Score       float64 `json:"score"`       // T(ctrl)
	Isolated    bool    `json:"isolated"`    // true after action a6 (Algo 6)
	IsolateTime int64   `json:"isolateTime"` // t_isolate (Eq 3.64)
	Updated     int64   `json:"updated"`     // last update timestamp
}

// MessageHashRecord is the direct V2BC message-hash submission `CID^m_i`
// (Eq 3.71). It is the ground truth that VerifyMessageIntegrity (Eq 3.18)
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
// (Eq 3.72, XV). Installed rules absent from this set indicate CC-DIM.
type FlowRuleRecord struct {
	DocType string `json:"docType"` // "flowrule"
	ID      string `json:"id"`      // v_i
	Rule    string `json:"rule"`    // endorsed flow-table entry
	Epoch   int64  `json:"epoch"`   // flow-table epoch
}

// AuditLog records `{v_i, t, CID_i, SHA3-256(L_i)}` for detection / DRL-action
// logs (Eq 3.65/3.66). The on-chain hash lets any peer verify the IPFS content.
type AuditLog struct {
	DocType string `json:"docType"` // "audit"
	ID      string `json:"id"`      // v_i
	T       int64  `json:"t"`       // event time
	CID     string `json:"cid"`     // IPFS CID of L_i
	Hash    string `json:"hash"`    // SHA3-256(L_i) (hex)
}

// IncidentLog is the immutable record of a DRL mitigation action
// (Algorithm 6, actions a6/a7/a8 and rekey logging Algo 1) plus optional CC
// impact metrics (NER/DER/PFER/RFR, RQ6).
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
// S^ctrl_CC-{TA,EA,SI,DIM} computed off-chain from IPFS logs (Eq 3.22-3.25) plus
// the extended composite S^ext_comp (Eq 3.26, incorporating the IPFS-augmented
// CC-TA term of Eq 3.68). Read by EvaluateControllerAC (Eq 3.59) and the
// controller trust update (Eq 3.61).
type CCSignatureRecord struct {
	DocType   string  `json:"docType"` // "ccsig"
	CtrlID    string  `json:"ctrlId"`
	T         int64   `json:"t"`
	SccTA     float64 `json:"sccTA"`     // S^ctrl_CC-TA (Eq 3.22 / augmented 3.68)
	SccEA     float64 `json:"sccEA"`     // S^ctrl_CC-EA (Eq 3.25)
	SccSI     float64 `json:"sccSI"`     // S^ctrl_CC-SI (Eq 3.23)
	SccDIM    float64 `json:"sccDIM"`    // S^ctrl_CC-DIM (Eq 3.24, 0 or 1)
	Composite float64 `json:"composite"` // S^ext_comp (Eq 3.26), computed off-chain
}

// IPFSAvailabilityRecord stores the availability ratio Q_IPFS(t) measured by the
// application plane on each peer (Eq 3.67). A drop below Q_th is itself a
// controller-compromise indicator (Eq 3.68) and drives conservative failover
// (Eq 3.69).
type IPFSAvailabilityRecord struct {
	DocType       string  `json:"docType"`       // "ipfsavail"
	T             int64   `json:"t"`             // measurement time
	QIPFS         float64 `json:"qIPFS"`         // Q_IPFS(t) in [0,1]
	ReachablePins int     `json:"reachablePins"` // |{n in N_pin : reachable}|
	NPin          int     `json:"nPin"`          // n_pin (required pin count)
	Degraded      bool    `json:"degraded"`      // Q_IPFS(t) < Q_th
}

// ChaincodeHash is the committed integrity reference H_CC = SHA3-256(chaincode
// bytes) (Eq 3.73). Peers verify their running code against it (Eq 3.74).
type ChaincodeHash struct {
	DocType    string `json:"docType"`    // "cchash"
	HCC        string `json:"hcc"`        // SHA3-256(chaincode bytes) (hex)
	CommitTime int64  `json:"commitTime"` // commit timestamp
}
