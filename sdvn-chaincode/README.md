# SMDAC SDVN Chaincode

Hyperledger Fabric chaincode implementing the **Shared Trust Substrate** of the
SMDAC framework (Section 3.3.5 of the project paper) — the blockchain + IPFS
layer that serves as both an independent controller-compromise (CC) detection
evidence source and an active mitigation enforcement mechanism.

It realises the paper's **two-layer endorsement model**:

| Layer | Realises | Crypto | Configured at |
|---|---|---|---|
| Fabric native endorsement policy (`k-of-n`) | "≥ k independent peers" of Eq 3.49 | ECDSA (Fabric default) | `--ccep "OutOf(k, ...)"` at deploy |
| Application-level Dilithium verify in chaincode | per-vehicle/controller non-repudiation (Eq 3.41/3.42/3.50/3.51/3.55) | ML-DSA / Dilithium (FIPS 204) | inside the functions in [`chaincode/`](chaincode/) |

PQC primitives use [`cloudflare/circl`](https://github.com/cloudflare/circl)
(`sign/mldsa/mldsa65`, the FIPS 204 standardised successor to Dilithium mode-3)
and `golang.org/x/crypto/sha3`.

## Layout

```
sdvn-chaincode/
├── main.go                     # registers the contract
├── go.mod
└── chaincode/
    ├── assets.go               # world-state structs (§1.1)
    ├── pqc.go                  # Dilithium verify + SHA3-256 (Eq 3.18/3.42)
    ├── pqc_test.go             # unit tests for the crypto helpers
    └── smartcontract.go        # all SMDAC chaincode functions (§1.2)
```

## What runs ON vs OFF the chaincode

Chaincode is deterministic and runs on every endorsing peer, so it only does
**verification, hashing, state read/write and comparison**. All
non-deterministic work is done by the off-chain application plane, which submits
only the *results* (CIDs / hashes / scores):

- IPFS add/pin with multi-pin replication `|N_pin| ≥ n_pin` (Eq 3.53).
- Kyber (ML-KEM) key-gen/enc/dec and the Kyber-LKH re-key tree (Eq 3.34–3.40, Algo 1).
- LLM nine-class inference and DRL policy π* (Eq 3.32/3.48).
- Computing the CC anomaly scores `S^ctrl_CC-*` from raw IPFS logs (Eq 3.22–3.25).

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

## Deploy onto test-network

Bring up the network with a CA + CouchDB (CouchDB enables the rich queries used
by phantom-identity / cross-verification helpers), then add a 3rd independent
stakeholder org so a real `k`-of-`n` policy is meaningful:

```bash
cd ../test-network
./network.sh up createChannel -ca -s couchdb        # 2-org network
cd addOrg3 && ./addOrg3.sh up -c mychannel -s couchdb && cd ..   # adds peer0.org3
```

Deploy with an explicit `k`-of-`n` endorsement policy (Eq 3.49). For `n=3`
orgs requiring `k=2`:

```bash
./network.sh deployCC \
  -ccn sdvncc \
  -ccp ../sdvn-chaincode \
  -ccl go \
  -ccep "OutOf(2, 'Org1MSP.peer', 'Org2MSP.peer', 'Org3MSP.peer')"
```

`OutOf(k, ...)` is the `≥ k` of Eq 3.49 at the Fabric layer — the single most
important flag for the paper's guarantee that *no single compromised peer can
unilaterally write*. A 4th stakeholder (Org4) can be added by copying
`addOrg3/` to `addOrg4/` and find-and-replacing `org3→org4` with bumped ports.

## Chaincode functions ↔ paper equations / algorithms

| Function | Paper source | Purpose |
|---|---|---|
| `RegisterVehicle` | Eq 3.55 (`tx_reg`) | Verify Dilithium sig over `pkD_i‖t_reg`, store VehicleIdentity in L_BC (controller-independent) |
| `EvaluateVehicleAC` | Eq 3.50 | `Dilithium.Verify ∧ T(v_i) ≥ τ_min ∧ v_i ∈ L_BC` |
| `UpdateTrustScore` | Eq 3.52 | EMA trust update, degrades on detection |
| `RevokeVehicle` | Algo 3 (`tx_rev`) | Revoke identity, trust → 0 |
| `SubmitMessageHash` | Eq 3.56 (`CID^m_i`) | Direct V2BC message-hash ground truth |
| `VerifyMessageIntegrity` | Eq 3.18 | Hash compare → DIM flag `S^{(i)}_DIM` |
| `WriteAuditLog` | Eq 3.53/3.54 | `{v_i,t,CID,hash}` audit record |
| `RegisterFlowRule` / `CrossVerifyFlowRule` | Eq 3.57 (`XV`) | Endorsed flow-rule store + cross-verify (`XV=0` ⇒ CC-DIM) |
| `RegisterControllerKey` / `GetControllerKey` | Eq 3.24/3.51 | Store/return `pk^{L_BC}_ctrl` + NB baseline |
| `RecordCCSignatures` | Eq 3.22–3.25 | Persist aggregated `S^ctrl_CC-*` |
| `EvaluateControllerAC` | Eq 3.51 | `S^ctrl_CC < θ_CC ∧ pk_used = pk^{L_BC}_ctrl ∧ |E_NB| < µ_NB+3σ_NB` |
| `DetectPhantomIdentities` | Eq 3.23 | Flow-table ids absent from L_BC |
| `ActivateStandbyController` | Algo 5 `a6` (`tx_fail`) | Endorsed standby handoff |
| `ReRegisterVehicles` | Algo 5 `a6` (`tx_rereg`) | Re-register ledger-verified vehicles |
| `FlowTablePurge` | Algo 5 `a7` (`tx_purge`) | Keep only ledger-endorsed flow rules |
| `LogIncident` / `RecordRekey` | Algo 1 & 5 `a8` (Eq 3.40) | Immutable DRL-action / rekey record |
| `GetAllVehicles` / `GetTrustScore` / `GetMessageHistory` | — | Read-only helpers |

## Application / gateway layer

The Direct V2BC channel (Fig 3.17 dashed arrows) and Algorithms 1/3/4/5 are
driven from the application plane via the **Fabric Gateway**. Base a thin
REST/gateway shim on `asset-transfer-basic/application-gateway-go` (or
`-typescript`) that exposes `/registerVehicle`, `/submitHash`, `/crossVerify`,
`/evaluateCtrlAC`, `/logIncident`, … endpoints for the NS-3 Python code to call.
