#!/bin/bash
# SDVN Blockchain Automated Setup Script
# Excludes: Step 6 (Queries), Step 8 (Teardown), 5F (Register Vehicle), 5G (Reselect Endorsers)

# Exit immediately if a critical command exits with a non-zero status
set -e

# Define root directory
HLF_DIR="$HOME/hlf/fabric-samples"

echo "=========================================================="
echo " STEP 1: Deep Clean & Reset Environment"
echo "=========================================================="
cd $HLF_DIR/test-network
./network.sh down || true

echo "Cleaning Docker containers and volumes..."
docker rm -f $(docker ps -aq) 2>/dev/null || true
docker volume prune -f
docker network prune -f
docker rmi $(docker images dev-* -q) 2>/dev/null || true

echo "Cleaning and vendoring chaincode dependencies..."
cd $HLF_DIR/sdvn-chaincode
rm -f sdvncc.tar.gz log.txt
export GOFLAGS="-buildvcs=false"
go mod tidy
go mod vendor

echo "=========================================================="
echo " STEP 2: Bring Up the 4-Org BFT Network"
echo "=========================================================="
cd $HLF_DIR/test-network
./network.sh up createChannel -c mychannel -ca -s couchdb

cd addOrg3
./addOrg3.sh up -c mychannel -s couchdb
cd ..

cd addOrg4
./addOrg4.sh up -c mychannel -s couchdb
cd ..

echo "Waiting 10 seconds for CouchDB instances to sync..."
sleep 10

echo "=========================================================="
echo " STEP 3: Deploy the SDVN Chaincode"
echo "=========================================================="
cd $HLF_DIR/sdvn-chaincode
export GOFLAGS="-buildvcs=false"
K=3 ORGS="Org1MSP.peer Org2MSP.peer Org3MSP.peer Org4MSP.peer" ./deploy.sh

echo "=========================================================="
echo " STEP 4: Configure CLI Environment Variables"
echo "=========================================================="
cd $HLF_DIR/test-network
export PATH=${PWD}/../bin:$PATH
export FABRIC_CFG_PATH=$PWD/../config/
export CORE_PEER_TLS_ENABLED=true
export CORE_PEER_LOCALMSPID="Org1MSP"
export CORE_PEER_TLS_ROOTCERT_FILE=${PWD}/organizations/peerOrganizations/org1.example.com/peers/peer0.org1.example.com/tls/ca.crt
export CORE_PEER_MSPCONFIGPATH=${PWD}/organizations/peerOrganizations/org1.example.com/users/Admin@org1.example.com/msp
export CORE_PEER_ADDRESS=localhost:7051

export ORDERER_CA=${PWD}/organizations/ordererOrganizations/example.com/orderers/orderer.example.com/msp/tlscacerts/tlsca.example.com-cert.pem
export ORG1_CA=${PWD}/organizations/peerOrganizations/org1.example.com/peers/peer0.org1.example.com/tls/ca.crt
export ORG2_CA=${PWD}/organizations/peerOrganizations/org2.example.com/peers/peer0.org2.example.com/tls/ca.crt
export ORG3_CA=${PWD}/organizations/peerOrganizations/org3.example.com/peers/peer0.org3.example.com/tls/ca.crt

echo "=========================================================="
echo " STEP 5: Write Data to the Ledger (Governance & Setup)"
echo "=========================================================="

echo "-> 5A. Initialize System Security Thresholds..."
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"SetSystemConfig","Args":["0.5", "0.7", "0.5", "0.8"]}'
sleep 4 # Brief pause to allow block to commit

echo "-> 5B. Seed the Trust-Based Endorser Set..."
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"SeedEndorserSet","Args":["[\"Org1MSP\",\"Org2MSP\",\"Org3MSP\",\"Org4MSP\"]", "all-seed", "1", "1718400000"]}'
sleep 4

echo "-> 5C. Anchor the Chaincode Hash..."
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"CommitChaincodeHash","Args":["e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "1718400000"]}'
sleep 4

echo "-> 5D. Register the Primary SDN Controller..."
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"RegisterControllerKey","Args":["CTRL_01", "Y3RybDFfcGtfYmFzZTY0", "100.0", "5.0"]}'
sleep 4

echo "-> 5E. Write an IPFS Audit Log..."
peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \
  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \
  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \
  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \
  -c '{"function":"WriteAuditLog","Args":["CTRL_01", "1718500200", "QmAuditLogCID987654321", "hash_of_audit_log_xyz"]}'
sleep 4

# echo "=========================================================="
# echo " STEP 7: Connect with NS3 Simulation (Start API Gateway)"
# echo "=========================================================="
# cd $HLF_DIR/fabric-rest-api

# echo "Enrolling Admin Identity..."
# node enrollAdmin.js

# echo "Starting Node.js REST API in the background..."
# # Run the node app in the background and output logs to api_gateway.log
# nohup node app.js > api_gateway.log 2>&1 &
# API_PID=$!

echo "=========================================================="
echo " SETUP COMPLETE! "
echo "=========================================================="
# echo "API Gateway is running in the background (PID: $API_PID)."
# echo "Logs for the API can be viewed using: tail -f $HLF_DIR/fabric-rest-api/api_gateway.log"
echo ""
echo "You can now run your NS-3 simulation in a new terminal window:"
echo "  cd ~/ns-allinone-3.35/ns-3.35"
echo "  ./waf --run scratch/DCA"
