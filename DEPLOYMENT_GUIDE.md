# SDVN Chaincode Deployment & Testing Guide

This guide provides the complete sequence of commands to set up the Hyperledger Fabric test network (with 4 organizations for BFT consensus), deploy the SDVN post-quantum chaincode, and execute test transactions.

---

## Step 1: Deep Clean & Reset Environment
*Run this if you have lingering Docker containers, corrupted CouchDB states, or need to start fresh from a previous failure.*

```bash
# 1. Navigate to the test network
cd ~/hlf/fabric-samples/test-network

# 2. Bring down the network gracefully
./network.sh down

# 3. Force remove any stuck or dead Fabric containers
docker rm -f $(docker ps -aq)

# 4. Wipe all corrupted CouchDB and peer ledger volumes (CRITICAL)
docker volume prune -f

# 5. Clear any dangling Docker networks
docker network prune -f
docker rmi $(docker images dev-* -q)

# 6. Clean the Chaincode Directory and re-vendor dependencies
cd ../sdvn-chaincode
rm -f sdvncc.tar.gz log.txt
export GOFLAGS="-buildvcs=false"
go mod tidy
go mod vendor
```

## Step 2: Bring Up the 4-Org BFT Network
*The architecture requires a Byzantine-fault-tolerant (BFT) threshold of exactly $k=3$ approvals out of $n=4$ peer organizations.*

```bash
cd ~/hlf/fabric-samples/test-network

# 1. Bring up the base network (Org1 & Org2) with CouchDB
./network.sh up createChannel -c mychannel -ca -s couchdb

# 2. Add Organization 3 to the channel
cd addOrg3
./addOrg3.sh up -c mychannel -s couchdb
cd ..

# 3. Add Organization 4 to the channel
cd addOrg4
./addOrg4.sh up -c mychannel -s couchdb
cd ..

# Pause briefly to let all CouchDB instances fully boot and sync
sleep 10
```

## Step 3: Deploy the SDVN Chaincode
*Deploys the smart contract across all 4 organizations using the strict OutOf(3, ...) BFT endorsement policy.*

```bash
cd ~/hlf/fabric-samples/sdvn-chaincode

# Ensure the Go build fix is applied
export GOFLAGS="-buildvcs=false"

# Deploy using the wrapper script targeting all 4 organizations
K=3 ORGS="Org1MSP.peer Org2MSP.peer Org3MSP.peer Org4MSP.peer" ./deploy.sh
```

## Step 4: Configure CLI Environment Variables
*To interact with the chaincode (Invoke/Query), configure your terminal to act as an administrative client for Org1.*

```bash
cd ~/hlf/fabric-samples/test-network

# Set global Fabric environment variables
export PATH=${PWD}/../bin:$PATH
export FABRIC_CFG_PATH=$PWD/../config/
export CORE_PEER_TLS_ENABLED=true
export CORE_PEER_LOCALMSPID="Org1MSP"
export CORE_PEER_TLS_ROOTCERT_FILE=${PWD}/organizations/peerOrganizations/org1.example.com/peers/peer0.org1.example.com/tls/ca.crt
export CORE_PEER_MSPCONFIGPATH=${PWD}/organizations/peerOrganizations/org1.example.com/users/Admin@org1.example.com/msp
export CORE_PEER_ADDRESS=localhost:7051

# Store TLS CA certificates for BFT multi-endorsement
export ORDERER_CA=${PWD}/organizations/ordererOrganizations/example.com/orderers/orderer.example.com/msp/tlscacerts/tlsca.example.com-cert.pem
export ORG1_CA=${PWD}/organizations/peerOrganizations/org1.example.com/peers/peer0.org1.example.com/tls/ca.crt
export ORG2_CA=${PWD}/organizations/peerOrganizations/org2.example.com/peers/peer0.org2.example.com/tls/ca.crt
export ORG3_CA=${PWD}/organizations/peerOrganizations/org3.example.com/peers/peer0.org3.example.com/tls/ca.crt
```

## Step 5: Write Data to the Ledger (Invoke)
*Write operations require gathering endorsements from at least 3 peers to satisfy the OutOf(3, ...) policy.*

> **Bootstrap order matters (SBE).** Run governance sub-steps **A → B → C in this exact order**. `SetSystemConfig` establishes the security thresholds *before* they are locked; `SeedEndorserSet` then binds the threshold and endorser-set keys to the trusted peer set `P` via **state-based endorsement (SBE)**. Running them out of order can lock out the first governance write. After sub-step B, any later write to the thresholds (`SetSystemConfig`) or the trusted set (`ReselectEndorsers`) requires a fresh `k`-of-`n` (3-of-4) endorsement from the peers in `P`.

### A. Initialize System Security Thresholds (Governance Setup)
Establishes the global thresholds `τ_min, θ_CC, τ_ctrl, Q_th` enforced by the access-control predicates (`EvaluateVehicleAC`, `EvaluateControllerAC`, etc.). Must be the **first** governance transaction — these functions error out until it is set.

```bash
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"SetSystemConfig","Args":["0.5", "0.7", "0.5", "0.8"]}'
```
*(Args order: `tauMin`, `thetaCC`, `tauCtrl`, `qTh`.)*

### B. Seed the Trust-Based Endorser Set (Governance Setup)
Selects the initial endorsing-peer set `P`. Because node trust scores are just initialized, use `all-seed` to start with **every** org as a peer (or `ta-seed` for a trusted-authority-chosen subset). This binds the `SystemConfig` and endorser-set keys to `P` via SBE.

```bash
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"SeedEndorserSet","Args":["[\"Org1MSP\",\"Org2MSP\",\"Org3MSP\",\"Org4MSP\"]", "all-seed", "1", "1718400000"]}'
```
*(Args order: `mspIDsJSON` (escaped JSON array), `bySel`, `epoch`, `ts`.)*

### C. Anchor the Chaincode Hash (Governance Setup)
Anchors code integrity (Eq 3.73) on-chain.

```bash
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"CommitChaincodeHash","Args":["e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "1718400000"]}'
```

### D. Register the Primary SDN Controller
Registers a controller with baseline Northbound API trust metrics.

```bash
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"RegisterControllerKey","Args":["CTRL_01", "Y3RybDFfcGtfYmFzZTY0", "100.0", "5.0"]}'
```

### E. Write an IPFS Audit Log
Simulates the gateway pinning an anomaly log to IPFS and saving the Hash/CID on-chain.

```bash
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"WriteAuditLog","Args":["CTRL_01", "1718500200", "QmAuditLogCID987654321", "hash_of_audit_log_xyz"]}'
```

### F. Register a Vehicle (Requires Valid ML-DSA-65 Signature)
Because the chaincode strictly enforces FIPS 204 byte-length checks, use the Go test-script to generate a mathematically valid post-quantum keypair and execute the command.

```bash
cd ~/hlf/fabric-samples/sdvn-chaincode
export GOFLAGS="-buildvcs=false"

# Run the test script to generate valid PQ keys and automatically output the terminal command
go run test-scripts/generate_test_data.go
```
(Copy and paste the peer chaincode invoke ... command generated by the script into your terminal).

### G. (Optional) Re-select Endorsing Peers at Runtime
Once node trust scores have diverged (via `UpdatePeerTrustScore`), re-select the trusted subset of peers instead of keeping everyone. `ReselectEndorsers` ranks all registered nodes by trust, takes the top `nPeers`, recomputes the BFT threshold `k = ⌊2n/3⌋+1`, and re-binds the SBE policy. This invoke must itself satisfy the current endorser-set SBE (3-of-4).

```bash
# Top-4 selection (keeps all orgs, k=3). Lower nPeers to drop low-trust nodes.
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"ReselectEndorsers","Args":["4", "2", "1718600000"]}'
```
*(Args order: `nPeers`, `epoch`, `ts`. Additional candidate nodes can be enrolled beforehand with `RegisterPeerNode`.)* After re-selection, refresh the committed chaincode endorsement policy (`--ccep`) to match `GetActiveEndorserSet` at the next governance window.

## Step 6: Read Data from the Ledger (Query)
Queries execute locally and instantly against peer0.org1.

### Get All Registered Vehicles (Full Details):
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetAllVehicles","Args":[]}'
```

### Get All Registered Vehicles (IDs Only):
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetRegisteredVehicleIDs","Args":[]}'
```

### Get All Registered Controllers (Full Details):
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetAllControllers","Args":[]}'
```

### Get All Registered Controllers (IDs Only):
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetRegisteredControllerIDs","Args":[]}'
```

### Get the Active Trust-Selected Endorser Set:
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetActiveEndorserSet","Args":[]}'
```

### Check a Peer Node's Trust Score:
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetPeerTrustScore","Args":["Org1MSP"]}'
```

### Check the Controller's Trust Score:
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetControllerTrustScore","Args":["CTRL_01"]}'
```

### Get Trust Scores for All Controllers:
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetAllControllerTrusts","Args":[]}'
```

### Retrieve the message hashes related to a specific vehicle:
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetMessageHistory","Args":["<VEHICLE_ID>"]}'
```

### Verify a Cross-Channel Message Hash:
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"VerifyMessageIntegrity","Args":["V_200", "1718500100", "hash_of_message_abc"]}'
```

### Retrieve the CC Signatures for a Specific Controller:
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetCCSignatures","Args":["CTRL_01"]}'
```

### Retrieve All DRL Mitigation Incidents:
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetAllIncidents","Args":[]}'
```

### Retrieve the Audit Logs for a Specific Vehicle:
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetVehicleAuditLogs","Args":["V_200"]}'
```

### Retrieve Global Security Thresholds (System Config):
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetPublicSystemConfig","Args":[]}'
```

## Step 7: Connect with NS3 Simulation:
*An intermediate Node.js REST API gateway is used to bridge the simulation and the blockchain.*

### Initialize the API Wallet and Admin Credentials:
```bash
cd ~/hlf/fabric-samples/fabric-rest-api
node enrollAdmin.js
```
Expected Output: A success message confirming the admin identity has been enrolled and the wallet/admin.id file has been generated.

### Start the API Gateway:
```bash
node app.js
```
Expected Output: `SDVN Blockchain REST API Gateway listening on port 3000`
(Leave this terminal running in the background).

### Execute the NS-3 Simulation:
```bash
cd ~/ns-allinone-3.35/ns-3.35
./waf --run scratch/DCA
```

### Watch the API terminal logs in real-time:
```bash
tail -f /tmp/fabric-api.log
```

### Read API terminal log file:
```bash
cat /tmp/fabric-api.log
```

## Step 8: Clean Teardown
*When you are finished testing, bring the network down gracefully.*

```bash
cd ~/hlf/fabric-samples/test-network
./network.sh down
```