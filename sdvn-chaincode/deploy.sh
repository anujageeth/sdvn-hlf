#!/usr/bin/env bash
#
# SPDX-License-Identifier: Apache-2.0
#
# Convenience wrapper that vendors the SMDAC chaincode and deploys it onto the
# test-network with a k-of-n endorsement policy (Eq 3.49).
#
# Usage:
#   ./deploy.sh                 # k=2 of Org1/Org2/Org3 (default)
#   K=3 ORGS="Org1MSP.peer Org2MSP.peer Org3MSP.peer Org4MSP.peer" ./deploy.sh
#
# Assumes the network and Org3 are already up:
#   cd ../test-network
#   ./network.sh up createChannel -ca -s couchdb
#   cd addOrg3 && ./addOrg3.sh up -c mychannel -s couchdb && cd ..
set -euo pipefail

# 1. Fix for Go 1.18+ VCS stamping error during packaging
export GOFLAGS="-buildvcs=false"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NETWORK_DIR="$(cd "${SCRIPT_DIR}/../test-network" && pwd)"

CC_NAME="${CC_NAME:-sdvncc}"
K="${K:-2}"
ORGS="${ORGS:-Org1MSP.peer Org2MSP.peer Org3MSP.peer}"

# 2. Build the OutOf(k,'A','B',...) policy string without spaces 
# to prevent the Fabric network.sh script from splitting arguments.
quoted=""
for o in $ORGS; do
  quoted="${quoted}${quoted:+,}'${o}'"
done
CCEP="OutOf(${K},${quoted})"

echo ">> Vendoring Go dependencies (circl, x/crypto) ..."
( cd "${SCRIPT_DIR}" && go mod tidy && go mod vendor )

echo ">> Deploying ${CC_NAME} with endorsement policy: ${CCEP}"
( cd "${NETWORK_DIR}" && ./network.sh deployCC \
    -ccn "${CC_NAME}" \
    -ccp "${SCRIPT_DIR}" \
    -ccl go \
    -ccep "${CCEP}" )

echo ">> Done. ${CC_NAME} deployed under '${CCEP}'."