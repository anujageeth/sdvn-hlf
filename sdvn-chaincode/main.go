/*
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"log"

	"github.com/hyperledger/fabric-contract-api-go/v2/contractapi"
	"github.com/hyperledger/fabric-samples/sdvn-chaincode/chaincode"
)

func main() {
	sdvnChaincode, err := contractapi.NewChaincode(&chaincode.SmartContract{})
	if err != nil {
		log.Panicf("Error creating sdvn chaincode: %v", err)
	}

	if err := sdvnChaincode.Start(); err != nil {
		log.Panicf("Error starting sdvn chaincode: %v", err)
	}
}
