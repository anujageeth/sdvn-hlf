/*
SPDX-License-Identifier: Apache-2.0

pqc.go wraps the post-quantum primitives used *inside* the chaincode.

The paper requires CRYSTALS-Dilithium (ML-DSA, FIPS 204) for non-repudiation
everywhere a vehicle/controller/peer signs (Eq 3.40/3.44/3.45/3.55/3.58/3.59/3.70)
and SHA3-256 for message-integrity hashing (Eq 3.18/3.65/3.66/3.71/3.73).

Only *verification* and *hashing* live on-chain — they are deterministic and
therefore safe to run on every endorsing peer. Key generation, Kyber (ML-KEM)
encapsulation and the Kyber-LKH re-key tree are performed off-chain by the
application plane; the chaincode only ever stores the resulting CIDs/hashes.

We use cloudflare/circl's ML-DSA-87 implementation, the FIPS 204 standardised
successor to Dilithium mode-5 (NIST Level 5). This MUST match the algorithm
the application plane actually signs with: DCA.cc's registration path
(register_vehicle_staggered / init_pqc_crypto) mints per-vehicle identity
keypairs via liboqs's OQS_SIG_alg_ml_dsa_87 and signs (pkD_i || tReg) with
that key before calling RegisterVehicle. A prior ML-DSA-65 verifier here
rejected every genuine ML-DSA-87 signature/key (wrong size and content),
which surfaced as "invalid registration signature" for every vehicle.
*/

package chaincode

import (
	"encoding/hex"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
	"golang.org/x/crypto/sha3"
)

// SHA3 returns the SHA3-256 digest of data (Eq 3.18/3.65/3.66/3.71/3.73).
func SHA3(data []byte) []byte {
	h := sha3.Sum256(data)
	return h[:]
}

// SHA3Hex returns the lowercase hex encoding of SHA3-256(data); this is the
// canonical on-chain hash representation stored in MessageHashRecord/AuditLog.
func SHA3Hex(data []byte) string {
	return hex.EncodeToString(SHA3(data))
}

// DilithiumVerify realizes Dilithium.Verify(pk, msg, sig) (Eq 3.45).
//
// It returns true iff sig is a valid ML-DSA-87 signature over msg under the
// public key encoded in pkBytes. An empty context string is used, matching the
// vehicle/controller signing convention in the application plane. Any decode
// failure is treated as a verification failure (never panics on-chain).
func DilithiumVerify(pkBytes, msg, sig []byte) bool {
	if len(pkBytes) != mldsa87.PublicKeySize || len(sig) != mldsa87.SignatureSize {
		return false
	}
	var pk mldsa87.PublicKey
	if err := pk.UnmarshalBinary(pkBytes); err != nil {
		return false
	}
	// ctx is empty; mldsa87.Verify(pk, msg, ctx, sig).
	return mldsa87.Verify(&pk, msg, nil, sig)
}
