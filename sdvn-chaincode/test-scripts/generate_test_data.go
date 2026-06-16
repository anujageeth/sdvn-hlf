package main

import (
	"encoding/base64"
	"fmt"
	"strconv"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
)

func main() {
	// 1. Generate real ML-DSA-65 (Dilithium) keys
	scheme := mldsa65.Scheme()
	pk, sk, err := scheme.GenerateKey()
	if err != nil {
		panic(err)
	}
	pkBytes, _ := pk.MarshalBinary()

	// 2. Prepare the exact message the chaincode expects: (pkD_i || t_reg)
	tReg := int64(1718500000)
	msg := append(append([]byte{}, pkBytes...), []byte(strconv.FormatInt(tReg, 10))...)

	// 3. Sign the message
	sig := scheme.Sign(sk, msg, nil)

	// 4. Encode to Base64 for the Fabric CLI
	pkB64 := base64.StdEncoding.EncodeToString(pkBytes)
	sigB64 := base64.StdEncoding.EncodeToString(sig)
	kyberB64 := base64.StdEncoding.EncodeToString([]byte("dummy_kyber_key"))

	// 5. Output the ready-to-use terminal command
	fmt.Println("\n=======================================================")
	fmt.Println("Copy and paste this exact command into your CLI terminal:")
	fmt.Println("=======================================================\n")
	
	fmt.Printf("peer chaincode invoke -o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile $ORDERER_CA -C mychannel -n sdvncc \\\n  --peerAddresses localhost:7051 --tlsRootCertFiles $ORG1_CA \\\n  --peerAddresses localhost:9051 --tlsRootCertFiles $ORG2_CA \\\n  --peerAddresses localhost:11051 --tlsRootCertFiles $ORG3_CA \\\n  -c '{\"function\":\"RegisterVehicle\",\"Args\":[\"V_200\", \"%s\", \"%s\", \"1718500000\", \"%s\"]}'\n\n", pkB64, kyberB64, sigB64)
}
