package cryptokey

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"testing"
)

func TestRejectsSmallOrderAndNoncanonicalTrustKeys(t *testing.T) {
	for _, text := range []string{
		"0000000000000000000000000000000000000000000000000000000000000000",
		"0100000000000000000000000000000000000000000000000000000000000000",
		"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05",
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a",
	} {
		key, err := hex.DecodeString(text)
		if err != nil {
			t.Fatal(err)
		}
		for _, sign := range []byte{0, 0x80} {
			key[31] = (key[31] & 0x7f) | sign
			if StrongEd25519PublicKey(key) {
				t.Fatal("accepted low-order key")
			}
		}
	}
	for tail := 0xed; tail <= 0xff; tail++ {
		key := bytes.Repeat([]byte{0xff}, 32)
		key[31] = 0x7f
		key[0] = byte(tail)
		for _, sign := range []byte{0, 0x80} {
			key[31] = (key[31] & 0x7f) | sign
			if StrongEd25519PublicKey(key) {
				t.Fatal("accepted noncanonical key")
			}
		}
	}
	for _, key := range []ed25519.PublicKey{nil, make([]byte, 31), make([]byte, 33)} {
		if StrongEd25519PublicKey(key) {
			t.Fatal("accepted wrong key size")
		}
	}
	for seed := byte(0); seed < 32; seed++ {
		key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, 32)).Public().(ed25519.PublicKey)
		before := append([]byte(nil), key...)
		if !StrongEd25519PublicKey(key) || !bytes.Equal(before, key) {
			t.Fatal("rejected/mutated generated key")
		}
	}
}
func TestIdentityKeyAcceptsForgeryWithoutScreening(t *testing.T) {
	key := make(ed25519.PublicKey, 32)
	key[0] = 1
	signature := make([]byte, 64)
	signature[0] = 1
	if !ed25519.Verify(key, []byte("arbitrary scope/domain"), signature) {
		t.Fatal("stdlib behavior changed; revisit weak-key regression")
	}
	if StrongEd25519PublicKey(key) {
		t.Fatal("forgery key accepted as trust")
	}
}
