/*
SPDX-License-Identifier: Apache-2.0
*/

package chaincode

import (
	"testing"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
	"github.com/stretchr/testify/require"
)

// TestDilithiumVerify checks that DilithiumVerify (Eq 3.45) accepts a genuine
// ML-DSA-65 signature and rejects a tampered message and a malformed key.
//
// It uses circl's stable sign.Scheme interface to generate keys and sign, then
// feeds the marshalled public key to DilithiumVerify exactly as the application
// plane would.
func TestDilithiumVerify(t *testing.T) {
	scheme := mldsa65.Scheme()
	pk, sk, err := scheme.GenerateKey()
	require.NoError(t, err)

	msg := []byte("v_i safety message")
	sig := scheme.Sign(sk, msg, nil) // nil opts => empty context, matching pqc.go

	pkBytes, err := pk.MarshalBinary()
	require.NoError(t, err)

	require.True(t, DilithiumVerify(pkBytes, msg, sig), "valid signature must verify")
	require.False(t, DilithiumVerify(pkBytes, []byte("tampered"), sig), "tampered message must fail")
	require.False(t, DilithiumVerify([]byte("not-a-key"), msg, sig), "malformed key must fail")
}

// TestSHA3 checks SHA3-256 hashing is deterministic and collision-sensitive.
func TestSHA3(t *testing.T) {
	a := SHA3Hex([]byte("hello"))
	b := SHA3Hex([]byte("hello"))
	c := SHA3Hex([]byte("hellp"))
	require.Equal(t, a, b, "SHA3 must be deterministic")
	require.NotEqual(t, a, c, "different inputs must hash differently")
	require.Len(t, a, 64, "SHA3-256 hex digest is 64 chars")
}
