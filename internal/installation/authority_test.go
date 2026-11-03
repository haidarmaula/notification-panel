package installation

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
)

type identityFixture struct {
	app  sqlc.Application
	last sqlc.ResolveMobileIdentityParams
}

func (f *identityFixture) GetApplicationByPublicID(_ context.Context, id pgtype.UUID) (sqlc.Application, error) {
	app := f.app
	app.PublicID = id
	return app, nil
}

func (f *identityFixture) ResolveMobileIdentity(_ context.Context, p sqlc.ResolveMobileIdentityParams) (int64, error) {
	f.last = p
	if p.Subject != "known" {
		return 0, pgx.ErrNoRows
	}
	return 42, nil
}

type originFixture struct {
	context OriginContext
}

func (f *originFixture) VerifyOrigin(_ context.Context, c OriginContext, evidence string) error {
	f.context = c
	if evidence != "fixture-evidence" {
		return ErrMobileOrigin
	}
	return nil
}

func TestMobileAuthorityPinsProjectAndEnrollmentOrigin(t *testing.T) {
	key, signer, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	project := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	other := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	origin := new(originFixture)
	policy := MobilePolicy{Issuer: "issuer", Audience: "app-a", Purpose: "mobile", Namespace: "namespace-a", VerificationKey: key, Origin: origin}
	otherPolicy := policy
	otherPolicy.Audience = "app-b"
	store := &identityFixture{app: sqlc.Application{ID: 9, Status: "ACTIVE"}}
	auth, err := NewMobileAuthority(store, map[pgtype.UUID]MobilePolicy{project: policy, other: otherPolicy})
	require.NoError(t, err)
	_, err = NewMobileAuthority(store, map[pgtype.UUID]MobilePolicy{project: policy, other: policy})
	require.ErrorIs(t, err, ErrConfiguration)
	mint := func(subject, purpose string, audience jwt.ClaimStrings, expires *jwt.NumericDate) string {
		t.Helper()
		raw, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, mobileClaims{Purpose: purpose, RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "issuer", Subject: subject, Audience: audience, ExpiresAt: expires,
		}}).SignedString(signer)
		require.NoError(t, err)
		return raw
	}
	ctx := context.Background()
	expiry := jwt.NewNumericDate(time.Now().Add(time.Minute))
	raw := mint("known", "mobile", jwt.ClaimStrings{"app-a"}, expiry)
	_, err = auth.Authenticate(ctx, other, raw)
	require.ErrorIs(t, err, ErrMobileCredential)
	_, err = auth.AuthenticateEnrollment(ctx, project, raw, "")
	require.ErrorIs(t, err, ErrMobileOrigin)
	_, err = auth.AuthenticateEnrollment(ctx, project, raw, "invalid")
	require.ErrorIs(t, err, ErrMobileOrigin)
	principal, err := auth.AuthenticateEnrollment(ctx, project, raw, "fixture-evidence")
	require.NoError(t, err)
	require.Equal(t, Principal{ApplicationID: 9, UserID: 42}, principal)
	require.Equal(t, sqlc.ResolveMobileIdentityParams{ApplicationID: 9, IdentityNamespace: "namespace-a", Subject: "known"}, store.last)
	require.Equal(t, OriginContext{ProjectID: project, Namespace: "namespace-a", Subject: "known", CredentialDigest: sha256.Sum256([]byte(raw))}, origin.context)
	for _, invalid := range []string{
		mint("known", "refresh", jwt.ClaimStrings{"app-a"}, expiry),
		mint("known", "mobile", jwt.ClaimStrings{"app-a", "app-b"}, expiry),
		mint("known", "mobile", jwt.ClaimStrings{"app-a"}, nil),
		mint("known", "mobile", jwt.ClaimStrings{"app-a"}, jwt.NewNumericDate(time.Now().Add(-time.Minute))),
		mint("unknown", "mobile", jwt.ClaimStrings{"app-a"}, expiry),
	} {
		_, err := auth.Authenticate(ctx, project, invalid)
		require.ErrorIs(t, err, ErrMobileCredential)
	}
	store.app.Status = "PROVISIONING"
	_, err = auth.Authenticate(ctx, project, raw)
	require.ErrorIs(t, err, ErrProjectInactive)
}
