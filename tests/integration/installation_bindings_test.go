//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/database"
	"hello/internal/database/sqlc"
	"hello/internal/features/applications"
	"hello/internal/installation"
)

func TestInstallationBindingSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	id := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		require.NoError(t, tx.QueryRow(ctx, query, args...).Scan(&n))
		return n
	}
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := tx.Exec(ctx, query, args...)
		require.NoError(t, err)
	}
	reject := func(code, query string, args ...any) {
		t.Helper()
		sub, err := tx.Begin(ctx)
		require.NoError(t, err)
		_, err = sub.Exec(ctx, query, args...)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		require.Equal(t, code, pgErr.Code)
		require.NoError(t, sub.Rollback(ctx))
	}
	a := id("INSERT INTO applications(key,name) VALUES('binding-schema-a','A') RETURNING id")
	b := id("INSERT INTO applications(key,name) VALUES('binding-schema-b','B') RETURNING id")
	userA := id("INSERT INTO users(application_id,external_id) VALUES($1,'binding-schema-user-a') RETURNING id", a)
	userB := id("INSERT INTO users(application_id,external_id) VALUES($1,'binding-schema-user-b') RETURNING id", b)
	nextOwner := id("INSERT INTO users(application_id,external_id) VALUES($1,'binding-schema-user-next') RETURNING id", a)

	var publicA pgtype.UUID
	require.NoError(t, tx.QueryRow(ctx, "SELECT public_id FROM applications WHERE id=$1", a).Scan(&publicA))
	q := sqlc.New(tx)
	originUse := sqlc.ConsumeMobileOriginParams{ApplicationPublicID: publicA, Issuer: "fixture-broker", NonceDigest: bytes.Repeat([]byte{3}, 32),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}}
	used, err := q.ConsumeMobileOrigin(ctx, originUse)
	require.NoError(t, err)
	require.EqualValues(t, 1, used)
	used, err = q.ConsumeMobileOrigin(ctx, originUse)
	require.NoError(t, err)
	require.Zero(t, used)
	originUse.NonceDigest = bytes.Repeat([]byte{4}, 32)
	originUse.ExpiresAt.Time = time.Now().Add(-time.Minute)
	used, err = q.ConsumeMobileOrigin(ctx, originUse)
	require.NoError(t, err)
	require.Zero(t, used)
	const mapping = "INSERT INTO mobile_identities(application_id,identity_namespace,subject,user_id) VALUES($1,'issuer-namespace','shared-subject',$2)"
	reject("23503", mapping, a, userB)
	exec(mapping, a, userA)
	exec(mapping, b, userB)
	resolved, err := sqlc.New(tx).ResolveMobileIdentity(ctx, sqlc.ResolveMobileIdentityParams{ApplicationID: b, IdentityNamespace: "issuer-namespace", Subject: "shared-subject"})
	require.NoError(t, err)
	require.Equal(t, userB, resolved)
	const install = `INSERT INTO mobile_installations(application_id,owner_user_id,proof_verifier)
		VALUES($1,$2,decode(repeat('ab',32),'hex')) RETURNING id`
	reject("23503", install, a, userB)
	installationA := id(install, a, userA)
	installationB := id(install, b, userB)
	const bind = `INSERT INTO installation_bindings(application_id,installation_id,owner_user_id,owner_epoch,
		platform,provider_namespace,provider_route_key,address_type,address_fingerprint,address_ciphertext,encryption_key_version)
		VALUES($1,$2,$3,$4,'ANDROID','shared-project','fixture-route','FCM_REGISTRATION_TOKEN',
		decode(repeat($5,32),'hex'),decode('01','hex'),1) RETURNING id`
	exec("SET CONSTRAINTS fk_binding_current_owner IMMEDIATE")
	reject("23503", bind, b, installationA, userB, int64(1), "aa")
	reject("23503", bind, a, installationA, nextOwner, int64(1), "aa")
	reject("23503", bind, a, installationA, userA, int64(2), "aa")
	first := id(bind, a, installationA, userA, int64(1), "aa")
	reject("23505", bind, b, installationB, userB, int64(1), "aa")
	reject("23505", bind, a, installationA, userA, int64(1), "bb")
	reject("23514", "UPDATE installation_bindings SET address_ciphertext=decode('02','hex') WHERE id=$1", first)
	reject("23514", "UPDATE mobile_installations SET owner_user_id=$1 WHERE id=$2", nextOwner, installationA)
	reject("23503", "UPDATE mobile_installations SET owner_user_id=$1,owner_epoch=2,version=2 WHERE id=$2", nextOwner, installationA)

	exec("UPDATE installation_bindings SET retired_at=now() WHERE id=$1", first)
	exec("UPDATE mobile_installations SET owner_user_id=$1,owner_epoch=2,version=2 WHERE id=$2", nextOwner, installationA)
	id(bind, a, installationA, nextOwner, int64(2), "aa")
	reject("23514", "UPDATE installation_bindings SET retired_at=NULL WHERE id=$1", first)
	require.Equal(t, userA, id("SELECT owner_user_id FROM installation_bindings WHERE id=$1", first))
	require.EqualValues(t, 1, id("SELECT owner_epoch FROM installation_bindings WHERE id=$1", first))
	actor := installation.Principal{ApplicationID: a, UserID: userA}
	otherActor := installation.Principal{ApplicationID: b, UserID: userB}
	route := installation.Route{Namespace: "shared-project", Key: "fixture-route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}
	protector, err := installation.NewProtector(1, map[int32][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	lifecycle, err := installation.NewLifecycle(installationLifecycleFixture{tx}, protector)
	require.NoError(t, err)
	_, err = lifecycle.Enroll(ctx, actor, route, "lifecycle-address")
	require.ErrorIs(t, err, installation.ErrProjectInactive)
	exec("ALTER TABLE applications DROP CONSTRAINT applications_status_check")
	exec("UPDATE applications SET status='ACTIVE' WHERE id IN ($1,$2)", a, b)
	enrolled, err := lifecycle.Enroll(ctx, actor, route, "lifecycle-address")
	require.NoError(t, err)
	require.NotEmpty(t, enrolled.Proof)
	claimed, err := lifecycle.Enroll(ctx, otherActor, route, "lifecycle-address")
	require.ErrorIs(t, err, installation.ErrAddressClaimed)
	require.Empty(t, claimed.Proof)
	_, err = lifecycle.Enroll(ctx, otherActor, route, "other-address")
	require.NoError(t, err)
	change := installation.Change{InstallationID: enrolled.State.PublicID, Proof: enrolled.Proof, ExpectedVersion: 1, Operation: installation.Refresh, Route: route, Address: "lifecycle-address"}
	_, err = lifecycle.Apply(ctx, otherActor, change)
	require.ErrorIs(t, err, installation.ErrNotFound)
	state, err := lifecycle.Apply(ctx, actor, change)
	require.NoError(t, err)
	require.EqualValues(t, 1, state.Version)
	change.Address = "other-address"
	_, err = lifecycle.Apply(ctx, actor, change)
	require.ErrorIs(t, err, installation.ErrAddressClaimed)
	change.Address = "lifecycle-address"
	state, err = lifecycle.Apply(ctx, actor, change)
	require.NoError(t, err)
	require.EqualValues(t, 1, state.Version)
	change.Address = "rotated-address"
	state, err = lifecycle.Apply(ctx, actor, change)
	require.NoError(t, err)
	require.EqualValues(t, 2, state.Version)
	require.EqualValues(t, 1, state.OwnerEpoch)
	change.Operation = installation.Logout
	_, err = lifecycle.Apply(ctx, actor, change)
	require.ErrorIs(t, err, installation.ErrVersion)
	change.Operation = installation.SwitchOwner
	change.ExpectedVersion = 2
	destination := installation.Principal{ApplicationID: a, UserID: nextOwner}
	state, err = lifecycle.Apply(ctx, destination, change)
	require.NoError(t, err)
	require.EqualValues(t, 3, state.Version)
	require.EqualValues(t, 2, state.OwnerEpoch)
	change.Operation = installation.Logout
	change.ExpectedVersion = 3
	_, err = lifecycle.Apply(ctx, actor, change)
	require.ErrorIs(t, err, installation.ErrNotFound)
	state, err = lifecycle.Apply(ctx, destination, change)
	require.NoError(t, err)
	require.EqualValues(t, 4, state.Version)
	require.EqualValues(t, 3, state.OwnerEpoch)
	require.EqualValues(t, 0, id(`SELECT count(*) FROM installation_bindings b JOIN mobile_installations i ON i.id=b.installation_id
		WHERE i.public_id=$1 AND b.retired_at IS NULL`, enrolled.State.PublicID))
	require.EqualValues(t, 3, id(`SELECT count(*) FROM installation_bindings b JOIN mobile_installations i ON i.id=b.installation_id
		WHERE i.public_id=$1 AND b.retired_at IS NOT NULL`, enrolled.State.PublicID))

	testMobileLifecycleHTTP(t, tx, publicA)

}

type installationLifecycleFixture struct{ pgx.Tx }

func (f installationLifecycleFixture) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	return f.Begin(ctx)
}

func testMobileLifecycleHTTP(t *testing.T, tx pgx.Tx, project pgtype.UUID) {
	t.Helper()
	mobileKey, mobileSigner, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	originKey, originSigner, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	cfg, err := json.Marshal(map[string]any{
		"current_key_version": 1, "fingerprint_key": bytes.Repeat([]byte{2}, 32),
		"encryption_keys": []map[string]any{{"version": 1, "key": bytes.Repeat([]byte{1}, 32)}},
		"projects": []map[string]any{{
			"app_id": project.String(), "issuer": "fixture-mobile", "audience": "fixture-a", "purpose": "access", "namespace": "issuer-namespace",
			"verification_key": []byte(mobileKey), "origin_issuer": "fixture-origin", "origin_audience": "fixture-enrollment", "origin_key": []byte(originKey),
			"routes": []map[string]any{{"platform": "ANDROID", "address_type": installation.FCMRegistrationToken,
				"provider_namespace": "shared-project", "provider_route_key": "fixture-route"}},
		}},
	})
	require.NoError(t, err)
	runtime, err := installation.LoadRuntime(bytes.NewReader(cfg), sqlc.New(tx), installationLifecycleFixture{tx})
	require.NoError(t, err)
	mux := http.NewServeMux()
	applications.RegisterMobileLifecycle(mux, runtime)
	now := time.Now()
	mobileJWT, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "fixture-mobile", "aud": "fixture-a", "sub": "shared-subject", "purpose": "access", "exp": now.Add(time.Minute).Unix(),
	}).SignedString(mobileSigner)
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(mobileJWT))
	origin, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "fixture-origin", "aud": "fixture-enrollment", "sub": "shared-subject", "purpose": "mobile_installation_enrollment",
		"app_id": project.String(), "namespace": "issuer-namespace", "credential_digest": base64.RawURLEncoding.EncodeToString(digest[:]),
		"jti": "fixture-mobile-http", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
	}).SignedString(originSigner)
	require.NoError(t, err)
	path := "/api/v1/apps/" + project.String() + "/mobile/installations"
	request := func(path string, data map[string]string, status int) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(data)
		require.NoError(t, err)
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+mobileJWT)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		require.Equal(t, status, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		return w
	}
	enroll := map[string]string{"platform": "ANDROID", "address_type": installation.FCMRegistrationToken, "address": "http-fixture-address", "origin": origin}
	enroll["provider_namespace"] = "client-override"
	request(path, enroll, 400)
	delete(enroll, "provider_namespace")
	w := request(path, enroll, 201)
	var result struct {
		Data map[string]string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.NotEmpty(t, result.Data["proof"])
	require.Equal(t, "1", result.Data["version"])
	request(path, enroll, 401)
	logout := map[string]string{"proof": result.Data["proof"], "version": "1"}
	w = request(path+"/"+result.Data["installation_id"]+"/logout", logout, 200)
	require.NotContains(t, w.Body.String(), "proof")
	require.Contains(t, w.Body.String(), `"owner_epoch":"2"`)
}
