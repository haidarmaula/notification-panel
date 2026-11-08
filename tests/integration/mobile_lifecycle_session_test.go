//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"hello/internal/features/applications"
	"hello/internal/installation"
)

func lifecycleSessionRuntime(t *testing.T, f executionFixture) (*installation.Runtime, ed25519.PrivateKey, ed25519.PrivateKey) {
	t.Helper()
	mobileKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{33}, 32))
	originKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{34}, 32))
	cfg, err := json.Marshal(map[string]any{
		"current_key_version": 1, "fingerprint_key": bytes.Repeat([]byte{2}, 32),
		"encryption_keys": []map[string]any{{"version": 1, "key": bytes.Repeat([]byte{1}, 32)}},
		"projects": []map[string]any{{
			"app_id": f.project.String(), "issuer": "mobile", "audience": "fixture-app", "purpose": "access", "namespace": "session-fixture",
			"verification_key": mobileKey.Public().(ed25519.PublicKey),
			"origin_issuer":    "origin", "origin_audience": "enroll", "origin_key": originKey.Public().(ed25519.PublicKey),
			"routes": []map[string]any{{"platform": "ANDROID", "address_type": installation.FCMRegistrationToken,
				"provider_namespace": "execution-fixture", "provider_route_key": "route"}},
		}},
	})
	require.NoError(t, err)
	runtime, err := installation.LoadRuntime(bytes.NewReader(cfg), f.q, f.pool)
	require.NoError(t, err)
	return runtime, mobileKey, originKey
}

func lifecycleSessionBearer(t *testing.T, key ed25519.PrivateKey, expires time.Time) string {
	t.Helper()
	raw, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "mobile", "aud": "fixture-app", "purpose": "access", "sub": "owner", "exp": expires.Unix(),
	}).SignedString(key)
	require.NoError(t, err)
	return raw
}

func lifecycleBlockedRequest(t *testing.T, ctx context.Context, f executionFixture, mux http.Handler, blocker pgx.Tx, token, suffix string, body map[string]string) <-chan *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	r := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/api/v1/apps/"+f.project.String()+"/mobile/installations"+suffix, bytes.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		result <- w
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		err := f.pool.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))",
			int32(blocker.Conn().PgConn().PID())).Scan(&waiting)
		return err == nil && waiting
	}, time.Second, 10*time.Millisecond, "the authenticated route must reach its database lock")
	return result
}

func lifecycleSessionDenied(t *testing.T, ctx context.Context, f executionFixture, result <-chan *httptest.ResponseRecorder) {
	t.Helper()
	select {
	case response := <-result:
		require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), f.enrollment.Proof)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var version, owner, installations, bindings int64
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT version,owner_user_id,
		(SELECT count(*) FROM mobile_installations),(SELECT count(*) FROM installation_bindings)
		FROM mobile_installations WHERE public_id=$1`, f.enrollment.State.PublicID).
		Scan(&version, &owner, &installations, &bindings))
	require.EqualValues(t, 1, version)
	require.Equal(t, f.user, owner)
	require.EqualValues(t, 2, installations)
	require.EqualValues(t, 2, bindings)
}

func TestMobileLifecycleRechecksSubjectMapping(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	runtime, mobileKey, originKey := lifecycleSessionRuntime(t, f)
	mux := http.NewServeMux()
	applications.RegisterMobileLifecycle(mux, runtime)
	var otherUser int64
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO users(application_id,external_id) VALUES($1,'other-session-user') RETURNING id", f.app).Scan(&otherUser))
	for _, removed := range []bool{false, true} {
		for _, operation := range []string{"enroll", "refresh", "logout", "switch", "state"} {
			t.Run(fmt.Sprintf("%s/removed=%t", operation, removed), func(t *testing.T) {
				actor, replacement := f.user, otherUser
				if operation == "switch" {
					actor, replacement = otherUser, f.user
				}
				executionSQL(t, ctx, f, `INSERT INTO mobile_identities(application_id,identity_namespace,subject,user_id)
					VALUES($1,'session-fixture','owner',$2) ON CONFLICT(application_id,identity_namespace,subject)
					DO UPDATE SET user_id=EXCLUDED.user_id`, f.app, actor)
				token := lifecycleSessionBearer(t, mobileKey, time.Now().Add(time.Minute))
				body := map[string]string{"proof": f.enrollment.Proof, "version": "1"}
				suffix := "/" + f.enrollment.State.PublicID.String() + "/" + operation
				switch operation {
				case "state":
					delete(body, "version")
				case "enroll", "refresh", "switch":
					body["platform"], body["address_type"], body["address"] = "ANDROID", installation.FCMRegistrationToken, "replacement-session-address"
				}
				if operation == "enroll" {
					suffix = ""
					delete(body, "proof")
					delete(body, "version")
					digest := sha256.Sum256([]byte(token))
					origin, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
						"iss": "origin", "aud": "enroll", "sub": "owner", "purpose": "mobile_installation_enrollment",
						"app_id": f.project.String(), "namespace": "session-fixture", "credential_digest": base64.RawURLEncoding.EncodeToString(digest[:]),
						"jti": fmt.Sprintf("session-enroll-%t", removed), "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
					}).SignedString(originKey)
					require.NoError(t, err)
					body["origin"] = origin
				}
				blocker, err := f.pool.Begin(ctx)
				require.NoError(t, err)
				defer blocker.Rollback(context.Background())
				_, err = blocker.Exec(ctx, "SELECT id FROM applications WHERE id=$1 FOR UPDATE", f.app)
				require.NoError(t, err)
				result := lifecycleBlockedRequest(t, ctx, f, mux, blocker, token, suffix, body)
				if removed {
					_, err = blocker.Exec(ctx, "DELETE FROM mobile_identities WHERE application_id=$1 AND identity_namespace='session-fixture' AND subject='owner'", f.app)
				} else {
					_, err = blocker.Exec(ctx, "UPDATE mobile_identities SET user_id=$2 WHERE application_id=$1 AND identity_namespace='session-fixture' AND subject='owner'", f.app, replacement)
				}
				require.NoError(t, err)
				require.NoError(t, blocker.Commit(ctx))
				lifecycleSessionDenied(t, ctx, f, result)
			})
		}
	}
}

func TestMobileLifecycleExpiresWhileWaitingForInstallation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	runtime, mobileKey, _ := lifecycleSessionRuntime(t, f)
	executionSQL(t, ctx, f, "INSERT INTO mobile_identities(application_id,identity_namespace,subject,user_id) VALUES($1,'session-fixture','owner',$2)", f.app, f.user)
	mux := http.NewServeMux()
	applications.RegisterMobileLifecycle(mux, runtime)
	expires := time.Now().Add(3 * time.Second).Truncate(time.Second)
	token := lifecycleSessionBearer(t, mobileKey, expires)
	blocker, err := f.pool.Begin(ctx)
	require.NoError(t, err)
	defer blocker.Rollback(context.Background())
	_, err = blocker.Exec(ctx, "SELECT id FROM mobile_installations WHERE public_id=$1 FOR UPDATE", f.enrollment.State.PublicID)
	require.NoError(t, err)
	result := lifecycleBlockedRequest(t, ctx, f, mux, blocker, token, "/"+f.enrollment.State.PublicID.String()+"/logout",
		map[string]string{"proof": f.enrollment.Proof, "version": "1"})
	var owner int64
	err = f.pool.QueryRow(ctx, "SELECT user_id FROM mobile_identities WHERE application_id=$1 AND subject='owner' FOR UPDATE NOWAIT", f.app).Scan(&owner)
	var locked *pgconn.PgError
	require.ErrorAs(t, err, &locked)
	require.Equal(t, "55P03", locked.Code, "the identity stays locked while installation work waits")
	require.Eventually(t, func() bool {
		var expired bool
		err := f.pool.QueryRow(ctx, "SELECT clock_timestamp()>=$1::timestamptz", expires).Scan(&expired)
		return err == nil && expired
	}, 4*time.Second, 10*time.Millisecond)
	require.NoError(t, blocker.Commit(ctx))
	lifecycleSessionDenied(t, ctx, f, result)
}
