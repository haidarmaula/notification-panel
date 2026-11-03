package installation

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallationProof(t *testing.T) {
	proof, verifier, err := NewProof()
	require.NoError(t, err)
	other, otherVerifier, err := NewProof()
	require.NoError(t, err)
	require.True(t, VerifyProof(proof, verifier[:]))
	require.False(t, VerifyProof(other, verifier[:]))
	require.False(t, VerifyProof(proof, otherVerifier[:]))
	require.False(t, VerifyProof(proof+"=", verifier[:]))
	require.False(t, VerifyProof(proof, verifier[:31]))
}

func TestProtectedAddressScopeAndRotation(t *testing.T) {
	oldKey := bytes.Repeat([]byte{1}, 32)
	newKey := bytes.Repeat([]byte{2}, 32)
	fingerprintKey := bytes.Repeat([]byte{3}, 32)
	old, err := NewProtector(1, map[int32][]byte{1: oldKey}, fingerprintKey)
	require.NoError(t, err)
	current, err := NewProtector(2, map[int32][]byte{1: oldKey, 2: newKey}, fingerprintKey)
	require.NoError(t, err)
	id := BindingIdentity{ApplicationID: 1, InstallationID: 2, BindingID: 3}
	other := BindingIdentity{ApplicationID: 4, InstallationID: 5, BindingID: 6}
	const address = "fixture-provider-address"
	sealed, err := old.Protect(id, "shared", FCMRegistrationToken, address)
	require.NoError(t, err)
	plaintext, err := current.Open(id, sealed.KeyVersion, sealed.Ciphertext)
	require.NoError(t, err)
	require.Equal(t, address, string(plaintext))
	for _, wrong := range []BindingIdentity{
		{ApplicationID: 4, InstallationID: 2, BindingID: 3},
		{ApplicationID: 1, InstallationID: 5, BindingID: 3},
		{ApplicationID: 1, InstallationID: 2, BindingID: 6},
	} {
		_, err = current.Open(wrong, sealed.KeyVersion, sealed.Ciphertext)
		require.ErrorIs(t, err, ErrCiphertext)
	}
	tampered := bytes.Clone(sealed.Ciphertext)
	tampered[len(tampered)-1] ^= 1
	_, err = current.Open(id, sealed.KeyVersion, tampered)
	require.ErrorIs(t, err, ErrCiphertext)
	_, err = current.Open(id, 2, sealed.Ciphertext)
	require.ErrorIs(t, err, ErrCiphertext)
	_, err = current.Open(id, 99, sealed.Ciphertext)
	require.ErrorIs(t, err, ErrCiphertext)

	same, err := current.Protect(other, "shared", FCMRegistrationToken, address)
	require.NoError(t, err)
	require.Equal(t, sealed.Fingerprint, same.Fingerprint)
	require.EqualValues(t, 2, same.KeyVersion)
	repeated, err := current.Protect(other, "shared", FCMRegistrationToken, address)
	require.NoError(t, err)
	require.NotEqual(t, same.Ciphertext, repeated.Ciphertext)
	differentNamespace, err := current.Protect(id, "other", FCMRegistrationToken, address)
	require.NoError(t, err)
	differentType, err := current.Protect(id, "shared", FCMFID, address)
	require.NoError(t, err)
	require.NotEqual(t, sealed.Fingerprint, differentNamespace.Fingerprint)
	require.NotEqual(t, sealed.Fingerprint, differentType.Fingerprint)

	_, err = NewProtector(2, map[int32][]byte{1: oldKey}, fingerprintKey)
	require.ErrorIs(t, err, ErrConfiguration)
	_, err = NewProtector(1, map[int32][]byte{1: fingerprintKey}, fingerprintKey)
	require.ErrorIs(t, err, ErrConfiguration)
}
