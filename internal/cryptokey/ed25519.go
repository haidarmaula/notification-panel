// Package cryptokey screens trusted cryptographic key configuration. It does not
// load keys, establish provenance or replace standard-library signature checks.
package cryptokey

import (
	"crypto/ed25519"
	"encoding/hex"
)

// StrongEd25519PublicKey rejects noncanonical y coordinates and all small-order
// points (including negative-zero aliases). This is encoding/weak-key screening,
// not complete curve/subgroup validation or proof of private-key possession.
// Non-curve encodings cannot pass crypto/ed25519.Verify. Keep the original key
// bytes for verification; never normalize a configured trust key.
//
// The five small-order y coordinates and sign masking follow libsodium 1.0.18:
// https://github.com/jedisct1/libsodium/blob/1.0.18/src/libsodium/crypto_core/ed25519/ref10/ed25519_ref10.c#L965-L1017
func StrongEd25519PublicKey(key ed25519.PublicKey) bool {
	if len(key) != ed25519.PublicKeySize {
		return false
	}
	var y [32]byte
	copy(y[:], key)
	y[31] &= 0x7f
	canonical := false
	for i := 31; i >= 0; i-- {
		primeByte := byte(0xff)
		if i == 31 {
			primeByte = 0x7f
		} else if i == 0 {
			primeByte = 0xed
		}
		if y[i] < primeByte {
			canonical = true
			break
		}
		if y[i] > primeByte {
			return false
		}
	}
	if !canonical {
		return false
	}
	switch hex.EncodeToString(y[:]) {
	case "0000000000000000000000000000000000000000000000000000000000000000",
		"0100000000000000000000000000000000000000000000000000000000000000",
		"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05",
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a":
		return false
	}
	return true
}
