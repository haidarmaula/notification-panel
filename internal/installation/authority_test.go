package installation

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
)

type identityFixture struct {
	app          sqlc.Application
	last         sqlc.ResolveMobileIdentityParams
	appCalls     int
	resolveCalls int
	checkContext func(context.Context)
}

func (f *identityFixture) GetApplicationByPublicID(ctx context.Context, id pgtype.UUID) (sqlc.Application, error) {
	f.appCalls++
	if f.checkContext != nil {
		f.checkContext(ctx)
	}
	app := f.app
	app.PublicID = id
	return app, nil
}

func (f *identityFixture) ResolveMobileIdentity(ctx context.Context, p sqlc.ResolveMobileIdentityParams) (int64, error) {
	f.resolveCalls++
	if f.checkContext != nil {
		f.checkContext(ctx)
	}
	f.last = p
	if p.Subject != "known" {
		return 0, pgx.ErrNoRows
	}
	return 42, nil
}

func signedRawJWT(t *testing.T, signer ed25519.PrivateKey, header, payload string) string {
	t.Helper()
	encodedHeader := base64.RawURLEncoding.EncodeToString([]byte(header))
	encodedPayload := base64.RawURLEncoding.EncodeToString([]byte(payload))
	input := encodedHeader + "." + encodedPayload
	signature := ed25519.Sign(signer, []byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

type originFixture struct {
	context      OriginContext
	checkContext func(context.Context)
}

func (f *originFixture) VerifyOrigin(ctx context.Context, c OriginContext, evidence string) error {
	if f.checkContext != nil {
		f.checkContext(ctx)
	}
	f.context = c
	if evidence != "fixture-evidence" {
		return ErrMobileOrigin
	}
	return nil
}

func TestMobileAuthorityBoundsIdentityAndOriginCalls(t *testing.T) {
	key, signer, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	project := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	credential, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, mobileClaims{
		Purpose: "mobile",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "issuer", Subject: "known", Audience: jwt.ClaimStrings{"app"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}).SignedString(signer)
	require.NoError(t, err)
	for _, name := range []string{"default deadline", "earlier caller deadline"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			var callerDeadline time.Time
			if name == "earlier caller deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
				callerDeadline, _ = ctx.Deadline()
			}
			var deadlines []time.Time
			check := func(ctx context.Context) {
				deadline, ok := ctx.Deadline()
				require.True(t, ok, "identity and origin calls need a server-side deadline")
				require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
				if !callerDeadline.IsZero() {
					require.Equal(t, callerDeadline, deadline)
				}
				deadlines = append(deadlines, deadline)
			}
			store := &identityFixture{app: sqlc.Application{ID: 9, Status: "ACTIVE"}, checkContext: check}
			origin := &originFixture{checkContext: check}
			authority, err := NewMobileAuthority(store, map[pgtype.UUID]MobilePolicy{
				project: {Issuer: "issuer", Audience: "app", Purpose: "mobile", Namespace: "users", VerificationKey: key, Origin: origin},
			})
			require.NoError(t, err)
			_, err = authority.AuthenticateEnrollment(ctx, project, credential, "fixture-evidence")
			require.NoError(t, err)
			require.Len(t, deadlines, 3)
			require.Equal(t, deadlines[0], deadlines[1], "identity resolution must share the original budget")
			require.Equal(t, deadlines[0], deadlines[2], "origin verification must share the original budget")
		})
	}
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
	session, err := auth.AuthenticateSession(ctx, project, raw)
	require.NoError(t, err)
	_, err = (&Lifecycle{}).EnrollSession(ctx, session, Route{}, "address")
	require.ErrorIs(t, err, ErrMobileOrigin, "a bearer alone cannot enter enrollment")
	session, err = auth.AuthenticateEnrollmentSession(ctx, project, raw, "fixture-evidence")
	require.NoError(t, err)
	require.True(t, session.enrollmentVerified)
	require.Equal(t, principal, session.Principal())
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

func TestMobileAuthorityRejectsWeakTrustKey(t *testing.T) {
	key := make(ed25519.PublicKey, 32)
	key[0] = 1
	project := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	authority, err := NewMobileAuthority(&identityFixture{}, map[pgtype.UUID]MobilePolicy{project: {Issuer: "issuer", Audience: "audience", Purpose: "mobile", Namespace: "namespace", VerificationKey: key, Origin: &originFixture{}}})
	require.ErrorIs(t, err, ErrConfiguration)
	require.Nil(t, authority)
}

func TestMobileAuthorityRejectsJWTFieldAliasesBeforeStoreAccess(t *testing.T) {
	key, signer, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	project := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	store := &identityFixture{app: sqlc.Application{ID: 9, Status: "ACTIVE"}}
	authority, err := NewMobileAuthority(store, map[pgtype.UUID]MobilePolicy{
		project: {Issuer: "issuer", Audience: "app", Purpose: "mobile", Namespace: "users", VerificationKey: key, Origin: &originFixture{}},
	})
	require.NoError(t, err)

	expires := time.Now().Add(time.Minute).Unix()
	payload := `{"iss":"issuer","sub":"known","aud":"app","exp":` + strconv.FormatInt(expires, 10) + `,"purpose":"mobile"}`
	for _, tc := range []struct {
		name   string
		header string
		body   string
	}{
		{name: "header case alias", header: `{"alg":"EdDSA","ALG":"EdDSA","typ":"JWT"}`, body: payload},
		{name: "uppercase-only claim name", header: `{"alg":"EdDSA","typ":"JWT"}`, body: strings.TrimSuffix(payload, "}") + `,"X-Claim":"unused"}`},
		{name: "payload case alias", header: `{"alg":"EdDSA","typ":"JWT"}`, body: strings.Replace(payload, `"sub":"known"`, `"sub":"known","SUB":"known"`, 1)},
		{name: "payload duplicate", header: `{"alg":"EdDSA","typ":"JWT"}`, body: strings.Replace(payload, `"purpose":"mobile"`, `"purpose":"mobile","purpose":"mobile"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credential := signedRawJWT(t, signer, tc.header, tc.body)
			_, err := authority.Authenticate(context.Background(), project, credential)
			require.ErrorIs(t, err, ErrMobileCredential)
			require.Zero(t, store.appCalls)
			require.Zero(t, store.resolveCalls)
		})
	}
}
