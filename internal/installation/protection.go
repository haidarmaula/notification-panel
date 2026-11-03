// Package installation protects mobile installation proofs and provider addresses.
package installation

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
)

const (
	secretSize           = 32
	maxAddressBytes      = 4096
	FCMFID               = "FCM_FID"
	FCMRegistrationToken = "FCM_REGISTRATION_TOKEN"
)

var (
	ErrConfiguration = errors.New("invalid installation protection configuration")
	ErrAddress       = errors.New("invalid provider address")
	ErrCiphertext    = errors.New("invalid protected provider address")
)

// NewProof returns a one-time credential and the verifier to persist.
func NewProof() (string, [sha256.Size]byte, error) {
	var secret [secretSize]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", [sha256.Size]byte{}, err
	}
	return base64.RawURLEncoding.EncodeToString(secret[:]), proofDigest(secret[:]), nil
}

func proofDigest(secret []byte) [sha256.Size]byte {
	return sha256.Sum256(append([]byte("hello:installation-proof:v1\x00"), secret...))
}

func VerifyProof(credential string, verifier []byte) bool {
	if len(credential) != base64.RawURLEncoding.EncodedLen(secretSize) || len(verifier) != sha256.Size {
		return false
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(credential)
	if err != nil || len(secret) != secretSize {
		return false
	}
	digest := proofDigest(secret)
	return subtle.ConstantTimeCompare(digest[:], verifier) == 1
}

// BindingIdentity is reserved before sealing and stays immutable.
type BindingIdentity struct {
	ApplicationID  int64
	InstallationID int64
	BindingID      int64
}

type ProtectedAddress struct {
	Ciphertext  []byte            `json:"-"`
	Fingerprint [sha256.Size]byte `json:"-"`
	KeyVersion  int32             `json:"-"`
}

// Protector writes with the current encryption key and reads retained versions.
// The fingerprint key is shared across applications in a provider namespace.
type Protector struct {
	keys           map[int32]cipher.AEAD
	current        int32
	fingerprintKey [secretSize]byte
}

func NewProtector(current int32, encryptionKeys map[int32][]byte, fingerprintKey []byte) (*Protector, error) {
	if current <= 0 || len(fingerprintKey) != secretSize {
		return nil, ErrConfiguration
	}
	p := &Protector{current: current, keys: make(map[int32]cipher.AEAD, len(encryptionKeys))}
	copy(p.fingerprintKey[:], fingerprintKey)
	for version, key := range encryptionKeys {
		if version <= 0 || len(key) != secretSize || hmac.Equal(key, fingerprintKey) {
			return nil, ErrConfiguration
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, ErrConfiguration
		}
		aead, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return nil, ErrConfiguration
		}
		p.keys[version] = aead
	}
	if p.keys[current] == nil {
		return nil, ErrConfiguration
	}
	return p, nil
}

func bindingAAD(id BindingIdentity, version int32) ([]byte, bool) {
	if id.ApplicationID <= 0 || id.InstallationID <= 0 || id.BindingID <= 0 || version <= 0 {
		return nil, false
	}
	aad := []byte("hello:binding-address:v1\x00")
	aad = binary.BigEndian.AppendUint64(aad, uint64(id.ApplicationID))
	aad = binary.BigEndian.AppendUint64(aad, uint64(id.InstallationID))
	aad = binary.BigEndian.AppendUint64(aad, uint64(id.BindingID))
	return binary.BigEndian.AppendUint32(aad, uint32(version)), true
}

func (p *Protector) Protect(id BindingIdentity, namespace, addressType, address string) (ProtectedAddress, error) {
	aad, ok := bindingAAD(id, p.current)
	if !ok || len(namespace) == 0 || len(namespace) > 255 || len(address) == 0 || len(address) > maxAddressBytes ||
		(addressType != FCMFID && addressType != FCMRegistrationToken) {
		return ProtectedAddress{}, ErrAddress
	}
	mac := hmac.New(sha256.New, p.fingerprintKey[:])
	mac.Write([]byte("hello:provider-address-fingerprint:v1\x00"))
	for _, part := range []string{namespace, addressType, address} {
		mac.Write(binary.BigEndian.AppendUint32(nil, uint32(len(part))))
		mac.Write([]byte(part))
	}
	out := ProtectedAddress{KeyVersion: p.current, Ciphertext: p.keys[p.current].Seal(nil, nil, []byte(address), aad)}
	copy(out.Fingerprint[:], mac.Sum(nil))
	return out, nil
}

func (p *Protector) Open(id BindingIdentity, version int32, ciphertext []byte) ([]byte, error) {
	aad, ok := bindingAAD(id, version)
	aead := p.keys[version]
	if !ok || aead == nil || len(ciphertext) <= aead.Overhead() || len(ciphertext) > maxAddressBytes+aead.Overhead() {
		return nil, ErrCiphertext
	}
	plaintext, err := aead.Open(nil, nil, ciphertext, aad)
	if err != nil {
		return nil, ErrCiphertext
	}
	return plaintext, nil
}
