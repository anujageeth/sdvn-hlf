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

### A. Anchor the Chaincode Hash (Governance Setup)
Must be executed immediately after deployment to anchor code integrity.

```bash
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"CommitChaincodeHash","Args":["e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "1718400000"]}'
```

### B. Register the Primary SDN Controller
Registers a controller with baseline Northbound API trust metrics.

```bash
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"RegisterControllerKey","Args":["CTRL_01", "Y3RybDFfcGtfYmFzZTY0", "100.0", "5.0"]}'
```

### C. Write an IPFS Audit Log
Simulates the gateway pinning an anomaly log to IPFS and saving the Hash/CID on-chain.

```bash
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"WriteAuditLog","Args":["CTRL_01", "1718500200", "QmAuditLogCID987654321", "hash_of_audit_log_xyz"]}'
```

### D. Register a Vehicle (Requires Valid ML-DSA-65 Signature)
Because the chaincode strictly enforces FIPS 204 byte-length checks, use the Go test-script to generate a mathematically valid post-quantum keypair and execute the command.

```bash
cd ~/hlf/fabric-samples/sdvn-chaincode
export GOFLAGS="-buildvcs=false"

# Run the test script to generate valid PQ keys and automatically output the terminal command
go run test-scripts/generate_test_data.go
```
(Copy and paste the peer chaincode invoke ... command generated by the script into your terminal).

## Step 6: Read Data from the Ledger (Query)
Queries execute locally and instantly against peer0.org1.

### Get All Registered Vehicles:
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetAllVehicles","Args":[]}'
```

### Check the Controller's Trust Score:
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"GetControllerTrustScore","Args":["CTRL_01"]}'
```

### Verify a Cross-Channel Message Hash:
```bash
peer chaincode query -C mychannel -n sdvncc -c '{"function":"VerifyMessageIntegrity","Args":["V_200", "1718500100", "hash_of_message_abc"]}'
```

## Step 7: Clean Teardown
*When you are finished testing, bring the network down gracefully.*

```bash
cd ~/hlf/fabric-samples/test-network
./network.sh down
```