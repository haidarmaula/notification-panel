package dispatch

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func replayCredentialFixture(t *testing.T) (*ReplayVerifier, ed25519.PrivateKey, ReplayRequest, replayClaims) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	verifier, err := NewReplayVerifier("fixture-operator", "fixture-replay", "fixture-deployment", public)
	require.NoError(t, err)
	request := replayTestRequest(t)
	grant := replayTestUUID(t, "40000000-0000-0000-0000-000000000001")
	hash, err := request.Fingerprint(7, grant)
	require.NoError(t, err)
	now := time.Now()
	claims := replayClaims{Purpose: "notification_replay", Deployment: "fixture-deployment", ApplicationID: request.ApplicationID.String(), RequestID: request.ID.String(), GrantID: grant.String(), Fingerprint: base64.RawURLEncoding.EncodeToString(hash[:]), AuthTime: jwt.NewNumericDate(now.Add(-20 * time.Second)), Methods: []string{"mfa"}, RegisteredClaims: jwt.RegisteredClaims{Issuer: "fixture-operator", Subject: "7", Audience: jwt.ClaimStrings{"fixture-replay"}, ID: "fixture-credential", IssuedAt: jwt.NewNumericDate(now.Add(-10 * time.Second)), ExpiresAt: jwt.NewNumericDate(now.Add(2 * time.Minute))}}
	// The caller cannot replace the pinned key by retaining the constructor's slice.
	clear(public)
	return verifier, private, request, claims
}

func signReplayCredential(t *testing.T, claims replayClaims, key ed25519.PrivateKey) string {
	t.Helper()
	credential, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(key)
	require.NoError(t, err)
	return credential
}

func TestReplayVerifierBindsCompleteRequest(t *testing.T) {
	verifier, key, request, claims := replayCredentialFixture(t)
	original := append([]ReplayPosition(nil), request.Positions...)
	credential := signReplayCredential(t, claims, key)
	verified, err := verifier.Verify(request, credential)
	require.NoError(t, err)
	require.Equal(t, original, request.Positions, "verification must preserve unsorted caller input")
	require.EqualValues(t, 7, verified.staffID)
	require.Equal(t, claims.GrantID, verified.grantID.String())
	require.Equal(t, claims.ExpiresAt.Time, verified.expiresAt)
	require.EqualValues(t, 0, verified.request.Positions[0].Partition)
	request.Positions[0], request.Positions[1] = request.Positions[1], request.Positions[0]
	_, err = verifier.Verify(request, credential)
	require.NoError(t, err, "position order does not change the signed selection")
	request.Positions[0].Offset++
	require.EqualValues(t, 7, verified.request.Positions[0].Offset, "verified selection must not alias caller memory")
	_, err = verifier.Verify(request, credential)
	require.ErrorIs(t, err, ErrReplayCredential)
	for _, change := range []func(*ReplayRequest){
		func(r *ReplayRequest) { r.Positions[0].Partition++ },
		func(r *ReplayRequest) { r.Positions[0].LogSourceID = r.ID },
	} {
		changed := replayTestRequest(t)
		change(&changed)
		before := append([]ReplayPosition(nil), changed.Positions...)
		_, err = verifier.Verify(changed, credential)
		require.ErrorIs(t, err, ErrReplayCredential)
		require.Equal(t, before, changed.Positions, "failed verification must preserve input")
	}
	request = replayTestRequest(t)
	request.ChangeReference = "INC-456"
	_, err = verifier.Verify(request, credential)
	require.ErrorIs(t, err, ErrReplayCredential)
}

func TestReplayVerifierRejectsInvalidAssertions(t *testing.T) {
	changes := map[string]func(*replayClaims){
		"issuer":                 func(c *replayClaims) { c.Issuer = "mobile-issuer" },
		"audience":               func(c *replayClaims) { c.Audience = jwt.ClaimStrings{"mobile"} },
		"multiple audiences":     func(c *replayClaims) { c.Audience = append(c.Audience, "mobile") },
		"purpose":                func(c *replayClaims) { c.Purpose = "staff_access" },
		"deployment":             func(c *replayClaims) { c.Deployment = "other-deployment" },
		"app":                    func(c *replayClaims) { c.ApplicationID = c.RequestID },
		"request":                func(c *replayClaims) { c.RequestID = c.ApplicationID },
		"actor mismatch":         func(c *replayClaims) { c.Subject = "8" },
		"grant mismatch":         func(c *replayClaims) { c.GrantID = c.RequestID },
		"auth after issuance":    func(c *replayClaims) { c.AuthTime = jwt.NewNumericDate(time.Now().Add(-5 * time.Second)) },
		"expiry before issuance": func(c *replayClaims) { c.ExpiresAt = jwt.NewNumericDate(c.IssuedAt.Add(-time.Second)) },
		"missing grant":          func(c *replayClaims) { c.GrantID = "" },
		"zero grant":             func(c *replayClaims) { c.GrantID = "00000000-0000-0000-0000-000000000000" },
		"noncanonical grant":     func(c *replayClaims) { c.GrantID = "40000000000000000000000000000001" },
		"wrong fingerprint": func(c *replayClaims) {
			c.Fingerprint = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
		},
		"padded fingerprint":    func(c *replayClaims) { c.Fingerprint += "=" },
		"subject":               func(c *replayClaims) { c.Subject = "007" },
		"missing subject":       func(c *replayClaims) { c.Subject = "" },
		"nonpositive subject":   func(c *replayClaims) { c.Subject = "0" },
		"missing jti":           func(c *replayClaims) { c.ID = "" },
		"missing issued at":     func(c *replayClaims) { c.IssuedAt = nil },
		"future issued at":      func(c *replayClaims) { c.IssuedAt = jwt.NewNumericDate(time.Now().Add(time.Minute)) },
		"missing auth time":     func(c *replayClaims) { c.AuthTime = nil },
		"future auth time":      func(c *replayClaims) { c.AuthTime = jwt.NewNumericDate(time.Now().Add(time.Minute)) },
		"stale auth time":       func(c *replayClaims) { c.AuthTime = jwt.NewNumericDate(time.Now().Add(-6 * time.Minute)) },
		"missing mfa":           func(c *replayClaims) { c.Methods = []string{"pwd"} },
		"missing expiry":        func(c *replayClaims) { c.ExpiresAt = nil },
		"expired":               func(c *replayClaims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) },
		"long lifetime":         func(c *replayClaims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(6 * time.Minute)) },
		"insufficient lifetime": func(c *replayClaims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(3 * time.Second)) },
		"future not before":     func(c *replayClaims) { c.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Minute)) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			verifier, key, request, claims := replayCredentialFixture(t)
			change(&claims)
			credential := signReplayCredential(t, claims, key)
			verified, err := verifier.Verify(request, credential)
			require.ErrorIs(t, err, ErrReplayCredential)
			require.Equal(t, VerifiedReplay{}, verified)
			require.NotContains(t, err.Error(), credential)
		})
	}
}

func TestReplayVerifierRejectsUntrustedSigningAndConfiguration(t *testing.T) {
	verifier, key, request, claims := replayCredentialFixture(t)
	_, other, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	for _, credential := range []string{"", strings.Repeat("x", 8193), "not-a-token", signReplayCredential(t, claims, other)} {
		_, err = verifier.Verify(request, credential)
		require.ErrorIs(t, err, ErrReplayCredential)
	}
	hmac, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("fixture-hmac"))
	require.NoError(t, err)
	_, err = verifier.Verify(request, hmac)
	require.ErrorIs(t, err, ErrReplayCredential)
	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)
	_, err = verifier.Verify(request, none)
	require.ErrorIs(t, err, ErrReplayCredential)
	credential := signReplayCredential(t, claims, key)
	var absent *ReplayVerifier
	_, err = absent.Verify(request, credential)
	require.ErrorIs(t, err, ErrReplayCredential)
	_, err = (&ReplayVerifier{}).Verify(request, credential)
	require.ErrorIs(t, err, ErrReplayCredential)
	for _, c := range []struct {
		issuer, audience, deployment string
		key                          ed25519.PublicKey
	}{
		{issuer: "", audience: "replay", deployment: "fixture", key: key.Public().(ed25519.PublicKey)},
		{issuer: "issuer", audience: "", deployment: "fixture", key: key.Public().(ed25519.PublicKey)},
		{issuer: "issuer", audience: "replay", deployment: "", key: key.Public().(ed25519.PublicKey)},
		{issuer: "issuer", audience: "replay", deployment: "fixture", key: nil},
	} {
		_, err = NewReplayVerifier(c.issuer, c.audience, c.deployment, c.key)
		require.ErrorIs(t, err, ErrReplayCredential)
	}
}
