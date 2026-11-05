package installation

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
)

type originUseFixture struct {
	calls        int
	result       int64
	checkContext func(context.Context)
}

func (f *originUseFixture) ConsumeMobileOrigin(ctx context.Context, p sqlc.ConsumeMobileOriginParams) (int64, error) {
	if f.checkContext != nil {
		f.checkContext(ctx)
	}
	f.calls++
	return f.result, nil
}

func TestSignedOriginBindsEvidenceBeforeConsuming(t *testing.T) {
	key, signer, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	store := &originUseFixture{result: 1}
	verifier, err := NewSignedOrigin(store, "broker", "enrollment", key)
	require.NoError(t, err)
	project := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	binding := OriginContext{ProjectID: project, Namespace: "users", Subject: "user", CredentialDigest: sha256.Sum256([]byte("jwt"))}
	now := time.Now()
	claims := originClaims{
		ProjectID: project.String(), Namespace: binding.Namespace, Purpose: originPurpose,
		CredentialDigest: base64.RawURLEncoding.EncodeToString(binding.CredentialDigest[:]),
		RegisteredClaims: jwt.RegisteredClaims{Issuer: "broker", Audience: jwt.ClaimStrings{"enrollment"}, Subject: "user", ID: "broker-jti",
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))},
	}
	mint := func(c originClaims) string {
		t.Helper()
		raw, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c).SignedString(signer)
		require.NoError(t, err)
		return raw
	}
	ctx := context.Background()
	raw := mint(claims)
	for _, invalid := range []OriginContext{
		{ProjectID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true}, Namespace: binding.Namespace, Subject: binding.Subject, CredentialDigest: binding.CredentialDigest},
		{ProjectID: project, Namespace: "other", Subject: binding.Subject, CredentialDigest: binding.CredentialDigest},
		{ProjectID: project, Namespace: binding.Namespace, Subject: "other", CredentialDigest: binding.CredentialDigest},
		{ProjectID: project, Namespace: binding.Namespace, Subject: binding.Subject, CredentialDigest: sha256.Sum256([]byte("other jwt"))},
	} {
		require.ErrorIs(t, verifier.VerifyOrigin(ctx, invalid, raw), ErrMobileOrigin)
	}
	tooLong := claims
	tooLong.ExpiresAt = jwt.NewNumericDate(now.Add(3 * time.Minute))
	require.ErrorIs(t, verifier.VerifyOrigin(ctx, binding, mint(tooLong)), ErrMobileOrigin)
	expired := claims
	expired.IssuedAt = jwt.NewNumericDate(now.Add(-time.Minute))
	expired.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Second))
	require.ErrorIs(t, verifier.VerifyOrigin(ctx, binding, mint(expired)), ErrMobileOrigin)
	require.Zero(t, store.calls)
	store.checkContext = func(ctx context.Context) {
		deadline, ok := ctx.Deadline()
		require.True(t, ok, "replay consumption needs a server-side deadline")
		require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
	}
	require.NoError(t, verifier.VerifyOrigin(ctx, binding, raw))
	store.result = 0
	require.ErrorIs(t, verifier.VerifyOrigin(ctx, binding, raw), ErrMobileOrigin)
	require.Equal(t, 2, store.calls)
}

func TestSignedOriginRejectsWeakTrustKey(t *testing.T) {
	key := make(ed25519.PublicKey, 32)
	key[0] = 1
	verifier, err := NewSignedOrigin(&originUseFixture{}, "issuer", "audience", key)
	require.ErrorIs(t, err, ErrConfiguration)
	require.Nil(t, verifier)
}
