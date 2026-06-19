# SMDAC SDVN Chaincode

Hyperledger Fabric chaincode implementing the **Shared Trust Substrate** of the
SMDAC framework (**Section 3.3.5, "Decentralized Blockchain and IPFS Design"** of
the project paper) — the blockchain + IPFS layer that serves *simultaneously* as
an independent controller-compromise (CC) detection evidence source **and** an
active mitigation enforcement mechanism, with neither role depending on any
controller-reported state.

> **Revision note.** This README tracks the latest paper revision in which the
> blockchain/IPFS design was consolidated into **§3.3.5 (Eqs 3.55–3.74)**. The
> endorsement model is now stated as a **provable Byzantine-fault-tolerant (BFT)
> threshold** rather than an arbitrary `k`-of-`n`, and the paper adds controller
> trust dynamics, standby/re-admission guards, an IPFS availability-protection
> subsystem, and chaincode-integrity governance. All equation references below
> use the **new numbering**.

It realises the paper's **two-layer endorsement model**:

| Layer | Realises | Crypto | Configured at |
|---|---|---|---|
| Fabric native endorsement policy (BFT `k`-of-`nₚ`) | `EP(tx)=⊤` of **Eq 3.55**, with the BFT threshold **Eq 3.56** | ECDSA (Fabric default) | `--ccep "OutOf(k, ...)"` at deploy |
| Application-level Dilithium verify in chaincode | per-vehicle / per-controller non-repudiation (**Eq 3.40 / 3.44 / 3.45 / 3.58 / 3.59 / 3.70**) | ML-DSA / Dilithium (FIPS 204) | inside the functions in [`chaincode/`](chaincode/) |

PQC primitives use [`cloudflare/circl`](https://github.com/cloudflare/circl)
(`sign/mldsa/mldsa65`, the FIPS 204 standardised successor to Dilithium mode-3)
and `golang.org/x/crypto/sha3` (SHA3-256 throughout, **Eq 3.40 / 3.65 / 3.66 /
3.71 / 3.73**).

---

## BFT endorsement threshold (Eq 3.55–3.57) — **read this before deploying**

A transaction commits to the immutable ledger only if it collects **≥ k**
verified Dilithium endorsements from the independent peer set
`P = {p₁,…,p_{nₚ}}` (**Eq 3.55**):

```
EP(tx)=⊤  ⇔  |{ pⱼ∈P : Dilithium.Verify(pkⱼ, tx, σⱼ)=⊤ }| ≥ k
```

The revised paper fixes `k` and the tolerated Byzantine count `f` to a strict
supermajority (**Eq 3.56**):

```
k = ⌊2nₚ/3⌋ + 1            f = ⌊(nₚ−1)/3⌋
```

| nₚ (peer orgs) | k (required) | f (Byzantine tolerated) |
|---|---|---|
| 3 | 3 (unanimous) | **0** |
| **4** | **3** | **1**  ← minimum for meaningful BFT |
| 5 | 4 | 1 |
| 7 | 5 | 2 |

**Consequence vs. the previous deployment:** the old `OutOf(2, …)` on 3 orgs
(`k=2`) no longer satisfies **Eq 3.56** — it tolerates a *minority* approval and
provides `f=0`. The revised model requires `nₚ ≥ 4, k=3` to tolerate a single
compromised/Byzantine peer.

> **Controller is a pure client (no peer).** The SDN controller runs **no Fabric
> peer** and is **never** part of the peer set `P`. It connects to the network
> only as a Gateway **client identity** (MSP role `client`), with transaction-
> submission rights and **zero** endorsement, ledger-hosting, or chaincode-install
> authority. Consequently a compromised controller cannot endorse, cannot reach
> the `k`-of-`nₚ` quorum, and cannot alter the trusted set or the security
> thresholds (both are bound to `P` by state-based endorsement — see *Runtime
> trust-based peer selection* below). The controller's only on-chain footprint is
> the transactions it submits, every one of which is independently re-verified by
> `P` and never taken as trusted input.

The **ordering service** is a *separate* node set `O`, disjoint from `P`, running
crash-fault-tolerant **Raft** with quorum `|O| ≥ 2f_O+1`, `f_O=⌊(|O|−1)/2⌋`
(**Eq 3.57**). Use **3 or 5 orderers**, never the single default orderer.

---

## Runtime trust-based peer selection (`peerselect.go`)

Rather than pinning **every** node as an endorsing peer — which is computationally
expensive and suboptimal — the endorsing set `P` is the **trusted subset** of
nodes, (re)selected *occasionally at runtime* by ranking on-chain node trust
scores `T(pⱼ)`. This is realised entirely on-chain in
[`chaincode/peerselect.go`](chaincode/peerselect.go).

**Node trust signal.** `RegisterPeerNode` seeds a neutral trust record per
candidate peer org (keyed by MSP id, e.g. `Org3MSP`); `UpdatePeerTrustScore`
applies the same EMA rule as the vehicle score (**Eq 3.60**) using a per-interval
well-behaved indicator (e.g. the peer endorsed honestly and passed `V_CC=1`,
**Eq 3.74**). Misbehaving nodes decay out of the next selection.

**Bootstrap (trust just initialized).** Because every `T(pⱼ)` starts at the
neutral value, trust-ranking is meaningless at `t=0`. The substrate is seeded
**once** via `SeedEndorserSet`, in either mode:

| `bySel` | Meaning |
|---|---|
| `all-seed` | **everyone** is a peer initially |
| `ta-seed` | a **trusted-authority**-chosen subset is the initial peer set |

**Runtime re-selection.** `ReselectEndorsers(nPeers, epoch, ts)` — re-run every
interval `Δ` — ranks all registered nodes by `T(pⱼ)` (descending, MSP-id
tie-break for deterministic write-sets across endorsers), takes the top `nPeers`
as the new `P`, and sizes the BFT threshold `k = ⌊2|P|/3⌋+1` (**Eq 3.56**).

**Enforcement (state-based endorsement).** Each (re)selection binds the
endorser-set singleton **and** the `SystemConfig` key with a `k`-of-`n`
**state-based endorsement (SBE)** policy over the selected MSPs (built as a
`SignaturePolicyEnvelope` in `buildThresholdEP`). So changing the trusted set or
the security thresholds requires a BFT quorum of the **currently** trusted peers —
and the next `ReselectEndorsers` is itself gated by the previous selection's
policy, so no single node (and no controller) can hijack the selection. The
application/gateway plane reads `GetActiveEndorserSet` to route endorsement
proposals only to the trusted peers (and an admin refreshes the committed
chaincode `--ccep` to match `P` at the next governance window).

| Function | Purpose |
|---|---|
| `RegisterPeerNode` / `GetPeerTrustScore` / `GetAllPeerTrust` | Seed & read node trust `T(pⱼ)` |
| `UpdatePeerTrustScore` | EMA node-trust update (**Eq 3.60** form) |
| `SeedEndorserSet` | One-time bootstrap (`all-seed` / `ta-seed`) |
| `ReselectEndorsers` | Periodic top-`nPeers` trust selection + BFT `k` + SBE re-bind |
| `GetActiveEndorserSet` | Current `P`, `k`, `n` for gateway routing / EP refresh |

---

## Layout

```
sdvn-chaincode/
├── main.go                     # registers the contract
├── go.mod
└── chaincode/
    ├── assets.go               # world-state structs (§"World-state assets" below)
    ├── pqc.go                  # Dilithium verify + SHA3-256 (Eq 3.40 / 3.45 / 3.65)
    ├── pqc_test.go             # unit tests for the crypto helpers
    ├── peerselect.go           # runtime trust-based peer selection + BFT SBE (Eq 3.55/3.56)
    └── smartcontract.go        # all SMDAC chaincode functions (table below)
```

---

## What runs ON vs OFF the chaincode

Chaincode is deterministic and runs on every endorsing peer, so it only does
**verification, SHA3-256 hashing, state read/write and comparison**. All
non-deterministic work is done by the off-chain application plane (hosted on the
endorsing peers, **Eq 3.33**), which submits only the *results* (CIDs / hashes /
scores / verdicts):

**ON-chain (deterministic chaincode):**
- Dilithium signature verification (**Eq 3.45**) and endorsement (**Eq 3.55**).
- Access-control predicates `AC(vᵢ,op)` (**Eq 3.58**) and `AC(ctrl,op)` (**Eq 3.59**).
- Trust-score EMA updates for vehicles (**Eq 3.60**) and the controller (**Eq 3.61**).
- SHA3-256 hashing, on-chain `CID`+`hash` writes (**Eq 3.66 / 3.71 / 3.73**),
  and integrity comparisons (**Eq 3.18 / 3.74**).
- Phantom-identity set difference `Ftable \ L_BC` (**Eq 3.23**) and
  cross-verification membership test `XV` (**Eq 3.72**).

**OFF-chain (application plane — submits results only):**
- IPFS `Add`/`Pin` with multi-pin replication `|N_pin| ≥ n_pin` (**Eq 3.65**).
- Kyber (ML-KEM) key-gen/enc/dec and the DKG-based broadcast re-key tree
  (**Eq 3.40–3.42, Algorithm 1**).
- LLM attack classification `P_CC` (**Eq 3.32**) and DRL policy `π*` (**Eq 3.51**).
- Computing the raw CC anomaly scores `S^ctrl_CC-*` from IPFS logs
  (**Eq 3.22–3.25**) and the extended composite (**Eq 3.26**).
- IPFS availability monitoring `Q_IPFS(t)` (**Eq 3.67**), the augmented CC-TA
  signal (**Eq 3.68**), and the conservative-failover trigger (**Eq 3.69**).

---

## World-state assets (`assets.go`)

| Asset | Key fields | Paper source |
|---|---|---|
| `VehicleIdentity` | `ID`, `PubKeyD` (Dilithium), `TrustScore`, `Registered`, `RegTime` | `L_BC`, Eq 3.70 |
| `MessageRecord` | `VehicleID`, `Timestamp`, `CID`, `MsgHash` (SHA3-256) | Eq 3.71 |
| `AuditLog` | `VehicleID`, `Timestamp`, `CID`, `LogHash` | Eq 3.66 |
| `FlowRule` | `VehicleID`, `RuleHash`, `Endorsed`, `Timestamp` | Eq 3.72 |
| `ControllerKey` | `CtrlID`, `PubKeyD`, `NB_mean`, `NB_std` (μ_NB, σ_NB baseline) | Eq 3.24 / 3.59 |
| `ControllerTrust` | `CtrlID`, `TrustScore`, `Isolated`, `IsolateTime` | Eq 3.61 / 3.64 |
| `CCSignatureRecord` | `S_CC_TA`, `S_CC_SI`, `S_CC_DIM`, `S_CC_EA`, `Composite` | Eq 3.22–3.26 |
| `IPFSAvailabilityRecord` | `Timestamp`, `Q_IPFS`, `ReachablePins`, `Degraded` | Eq 3.67 |
| `ChaincodeHash` | `H_CC` (SHA3-256 of deployed bytes), `CommitTime` | Eq 3.73 |
| `IncidentRecord` | `Timestamp`, `Action` (a₆/a₇/a₈), `CID`, `LogHash` | Eq 3.66, Algo 6 |

---

## Chaincode functions ↔ paper equations / algorithms (updated numbering)

### Vehicle identity, access control & trust

| Function | Paper source | Purpose |
|---|---|---|
| `RegisterVehicle` | **Eq 3.70** (`tx_reg`) | Verify Dilithium sig over `pkᴰ‖t_reg`, store `VehicleIdentity` in `L_BC` — submitted **directly to peers**, bypassing the controller |
| `EvaluateVehicleAC` | **Eq 3.58** | `Dilithium.Verify ∧ T(vᵢ) ≥ τ_min ∧ vᵢ ∈ L_BC` (all three jointly) |
| `UpdateTrustScore` | **Eq 3.60** | Vehicle EMA trust: `T⁽ᵗ⁺¹⁾ = λT⁽ᵗ⁾ + (1−λ)·𝟙[S_comp < θ_adapt]` |
| `RevokeVehicle` | Algo 5 | Revoke identity, set `TrustScore → 0` |
| `GetAllVehicles` / `GetTrustScore` | — | Read-only helpers |

### Direct V2BC interface & message integrity

| Function | Paper source | Purpose |
|---|---|---|
| `SubmitMessageHash` | **Eq 3.71** (`CIDᵐᵢ`) | Direct V2BC ground truth: store `{vᵢ, ts, CIDᵐ}` for the IPFS record `SHA3-256(mᵢ)‖σᵢ‖ts` |
| `VerifyMessageIntegrity` | **Eq 3.18** | Compare controller-forwarded `mᵢ` against on-chain hash → DIM flag `S⁽ⁱ⁾_DIM` |
| `GetMessageHistory` | — | Read-only message-hash history |
| `WriteAuditLog` | **Eq 3.65 / 3.66** | Persist `{vᵢ, t, CID, SHA3-256(Lᵢ)}` audit record under `EP(tx)=⊤` |

### Flow rules & cross-verification

| Function | Paper source | Purpose |
|---|---|---|
| `RegisterFlowRule` | **Eq 3.72** | Store an endorsed flow-rule record |
| `CrossVerifyFlowRule` | **Eq 3.72** (`XV`) | `XV(vᵢ,t)=𝟙[F_table(vᵢ) ∈ Chain.Query(P,vᵢ,t)]`; `XV=0` ⇒ CC-DIM indicator |

### Controller legitimacy, keys & CC scoring

| Function | Paper source | Purpose |
|---|---|---|
| `RegisterControllerKey` / `GetControllerKey` | **Eq 3.24 / 3.59** | Store/return `pk^{L_BC}_ctrl` and the northbound `(μ_NB, σ_NB)` baseline |
| `RecordCCSignatures` | **Eq 3.22–3.26** | Persist aggregated `S^ctrl_{CC-TA, CC-SI, CC-DIM, CC-EA}` and composite |
| `EvaluateControllerAC` | **Eq 3.59** | `S^ctrl_CC < θ_CC ∧ pk_used = pk^{L_BC}_ctrl ∧ |E_NB| < μ_NB+3σ_NB` |
| `UpdateControllerTrustScore` | **Eq 3.61 / 3.62** | **(new)** Controller EMA trust, re-evaluated every interval Δ from `L_BC`/`N_pin` only |
| `DetectPhantomIdentities` | **Eq 3.23** | Flow-table ids absent from `L_BC` (`Ftable \ L_BC`) |

### Failover, re-admission & DRL-action records (Algorithm 6)

| Function | Paper source | Purpose |
|---|---|---|
| `GuardStandbyController` | **Eq 3.63** | **(new)** Standby activation guard: `pk_standby ∈ L_BC ∧ T(ctrl_standby) ≥ τ_ctrl ∧ S_CC = 0` |
| `ActivateStandbyController` | Algo 6 `a₆` (`tx_fail`) | Endorsed standby handoff — **gated by `GuardStandbyController`** |
| `ReRegisterVehicles` | Algo 6 `a₆` (`tx_rereg`) | Re-register ledger-verified vehicles to the standby |
| `FlowTablePurge` | Algo 6 `a₇` (`tx_purge`) | Restore flow table to ledger-endorsed entries only |
| `ReadmitController` | **Eq 3.64** | **(new)** Re-admit an isolated controller after `t_now − t_isolate ≥ T_excl ∧ Guard=⊤ ∧ T(ctrl) ≥ τ_ctrl` |
| `LogIncident` | **Eq 3.66**, Algo 6 `a₈` | Immutable DRL-action record `{t, a*, CID, SHA3-256(L_incident)}` |
| `RecordRekey` | **Eq 3.40–3.42**, Algo 1 | Record `{G′, SHA3-256(K_G′), CID}` of a DKG re-key |

### IPFS availability protection (new subsystem)

| Function | Paper source | Purpose |
|---|---|---|
| `RecordIPFSAvailability` | **Eq 3.67** | **(new)** Store `Q_IPFS(t)=|reachable N_pin|/n_pin`; flag `Q_IPFS < Q_th` |
| `EvaluateConservativeFailover` | **Eq 3.69** | **(new)** `𝟙[Q_IPFS < Q_th ∧ T_stale > T_cache^max]` → force `a₆` isolation |

### Chaincode-integrity governance (new)

| Function | Paper source | Purpose |
|---|---|---|
| `CommitChaincodeHash` | **Eq 3.73** | **(new)** Commit `H_CC = SHA3-256(chaincode bytes)` at init under `EP(tx)=⊤` |
| `VerifyChaincodeIntegrity` | **Eq 3.74** | **(new)** `V_CC(pⱼ)=𝟙[SHA3-256(running code) = H_CC]`; mismatch ⇒ peer excluded from quorum & flagged CC-DIM |

---

## IPFS implementation

IPFS is the **off-chain half of the Shared Trust Substrate**. Detailed traffic
and detection logs are stored in a **distributed IPFS cluster**; only the
lightweight cryptographic proof (`CID` + `SHA3-256` hash) is committed on-chain.
This keeps the ledger small while making every audit log independently
verifiable and tamper-evident.

### Write path — `Add → Pin → Chain.Write` (Eq 3.65, 3.66)

```
CIDᵢ      = IPFS.Add(Lᵢ)                              # content-addressed (Eq 3.65)
            IPFS.Pin(CIDᵢ, N_pin),  |N_pin| ≥ n_pin   # multi-pin replication (Eq 3.65)
Chain.Write({ vᵢ, t, CIDᵢ, SHA3-256(Lᵢ) })           # proof only, EP(tx)=⊤ (Eq 3.66)
```

- The full log `Lᵢ` never touches the ledger — only its `CID` and `SHA3-256`
  digest do (chaincode functions `WriteAuditLog`, `LogIncident`).
- **Multi-pin replication** `|N_pin| ≥ n_pin` pins each `CID` across ≥ `n_pin`
  **independent** pinning nodes operated separately from the SDN controller, so
  logs survive even if some IPFS nodes collude with a compromised controller.
- Any peer re-derives `SHA3-256` of the fetched object and compares it to the
  on-chain hash (**Eq 3.74**-style check), so integrity holds **without trusting
  the node that submitted the data**.

### Direct V2BC message-hash submission (Eq 3.71)

Vehicles submit message hashes **directly** to the IPFS cluster, bypassing the
controller, to establish a tamper-evident ground truth:

```
CIDᵐᵢ = IPFS.Add( SHA3-256(mᵢ) ‖ σᵢ ‖ ts )          # σᵢ = Dilithium signature
        Chain.Write(vᵢ, ts, CIDᵐᵢ)                  # via SubmitMessageHash
```

`VerifyMessageIntegrity` (**Eq 3.18**) later compares the controller-forwarded
copy of `mᵢ` against this blockchain-anchored ground truth.

### Availability protection — three mechanisms

A compromised controller with full network visibility could try to degrade IPFS
pinning nodes to starve CC detection of evidence. The paper defines three
self-contained defences:

1. **IPFS degradation *is* a CC signal (Eq 3.67 → 3.68).** The application plane
   on each peer independently measures the availability ratio
   `Q_IPFS(t) = |{n ∈ N_pin : reachable}| / n_pin ∈ [0,1]`
   (recorded on-chain via `RecordIPFSAvailability`). A drop below `Q_th` adds a
   weighted term to the CC-TA score:
   `S^aug_CC-TA = S^ctrl_CC-TA + w_Q·𝟙[Q_IPFS < Q_th]` — a controller degrading
   its own evidence source is itself evidence of compromise. `S^aug_CC-TA`
   replaces `S^ctrl_CC-TA` in the extended composite (**Eq 3.26**).

2. **Endorsing-peer local cache.** Each peer keeps a rolling cache of CC
   audit-log hashes for the last `T_cache` seconds. When IPFS is unreachable the
   application plane falls back to this cache, whose integrity is itself
   guaranteed by the `k`-of-`nₚ` endorsement policy.

3. **Conservative failover (Eq 3.69).** When IPFS *and* the cache are both stale
   beyond `T_cache^max`:
   `Failover(t) = 𝟙[ Q_IPFS < Q_th ∧ T_stale > T_cache^max ]` forces the DRL
   agent to select `a₆` (controller isolation) regardless of `P_CC` confidence
   (`EvaluateConservativeFailover`).

### Deploying the IPFS cluster

The chaincode itself does **no** IPFS I/O (it must stay deterministic). The
cluster is provisioned alongside the application plane:

```bash
# n_pin independent pinning nodes (run on hosts disjoint from the controller).
# Recommended: ipfs-cluster for coordinated multi-pin replication.
ipfs-cluster-service init
ipfs-cluster-service daemon            # repeat on each of the n_pin nodes; peer them
ipfs-cluster-ctl peers ls              # confirm |N_pin| ≥ n_pin before going live
```

The application/gateway shim (below) calls the IPFS HTTP API to `add`/`pin`,
then submits the returned `CID` + `SHA3-256` to chaincode.

---

## Build / vendor (required before deploy)

> ⚠️ Go was not available in the environment where these files were authored, so
> `go.sum` and the `vendor/` tree were **not** generated. Run this once with the
> Go toolchain installed (Go ≥ 1.23):

```bash
cd sdvn-chaincode
go mod tidy        # resolves circl + x/crypto and writes go.sum
go vet ./...       # optional sanity check
go test ./...      # runs the PQC unit tests
go mod vendor      # Fabric's Go chaincode lifecycle expects a vendor/ tree
```

---

## Deploy onto test-network

Bring up the network with a CA + CouchDB (CouchDB enables the rich queries used
by phantom-identity / cross-verification helpers). For a **BFT-meaningful**
deployment satisfying **Eq 3.56** you need **nₚ = 4** peer orgs (`k=3`, `f=1`),
so add Org3 **and** Org4. Use a multi-orderer Raft service (**Eq 3.57**).

```bash
cd ../test-network
./network.sh up createChannel -ca -s couchdb        # 2-org base
cd addOrg3 && ./addOrg3.sh up -c mychannel -s couchdb && cd ..   # adds peer0.org3
# Copy addOrg3/ → addOrg4/, find-and-replace org3→org4 with bumped ports, then:
cd addOrg4 && ./addOrg4.sh up -c mychannel -s couchdb && cd ..   # adds peer0.org4
```

Deploy with the BFT supermajority endorsement policy (`k = ⌊2nₚ/3⌋+1`). For
**nₚ = 4 ⇒ k = 3**:

```bash
./network.sh deployCC \
  -ccn sdvncc \
  -ccp ../sdvn-chaincode \
  -ccl go \
  -ccep "OutOf(3, 'Org1MSP.peer', 'Org2MSP.peer', 'Org3MSP.peer', 'Org4MSP.peer')"
```

`OutOf(k, …)` is the BFT threshold of **Eq 3.55/3.56** at the Fabric layer — the
single most important flag for the paper's guarantee that *no single compromised
peer can unilaterally write*. With `k=3, nₚ=4` the network tolerates **f=1**
Byzantine peer. Scale up by copying `addOrgN/` with bumped ports and recomputing
`k` from the table above.

> **Post-deploy bootstrap order (matters for the SBE binding).** Run these once,
> **in this order**, so the state-based endorsement policy never locks out its own
> first write:
>
> 1. `SetSystemConfig` — establish the security thresholds *before* they are bound.
> 2. `SeedEndorserSet` — `all-seed` (everyone) or `ta-seed` (trusted-authority
>    subset); this binds the endorser-set and `SystemConfig` keys to `P` via SBE.
> 3. `CommitChaincodeHash` — anchor `H_CC` (below).
>
> Thereafter call `ReselectEndorsers` every interval `Δ` to refresh `P` from node
> trust, and refresh the committed `--ccep` to match `GetActiveEndorserSet` at the
> next governance window.

> **Chaincode-governance hardening (Eq 3.73/3.74).** Immediately after deploy,
> invoke `CommitChaincodeHash` to anchor `H_CC` on-chain, and have peers run
> `VerifyChaincodeIntegrity` each window. Chaincode install/upgrade authority
> must be restricted to the peer set `P` (governance threshold
> `k_G = ⌊2nₚ/3⌋+1`); **the controller is granted transaction-submission rights
> only and zero chaincode-installation authority.**

---

## Application / gateway layer

The Direct V2BC channel (Fig 3.17 dashed arrows) and Algorithms 1/5/6 are driven
from the application plane via the **Fabric Gateway**. Base a thin REST/gateway
shim on `asset-transfer-basic/application-gateway-go` (or `-typescript`) that
exposes endpoints for the NS-3 Python code to call, e.g. `/registerVehicle`
(3.70), `/submitHash` (3.71), `/crossVerify` (3.72), `/evaluateCtrlAC` (3.59),
`/updateCtrlTrust` (3.61), `/recordIPFSAvail` (3.67), `/activateStandby` (3.63),
`/readmitController` (3.64), `/logIncident` (3.66), `/recordRekey` (Algo 1), …

The gateway shim is also where IPFS `add`/`pin` happens (the chaincode only ever
receives the resulting `CID` + `SHA3-256` hash).