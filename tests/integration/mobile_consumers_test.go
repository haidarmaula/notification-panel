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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"hello/internal/database"
	"hello/internal/database/sqlc"
	"hello/internal/features/applications"
	"hello/internal/installation"
)

// Two independent HTTP clients exercise the generic lifecycle contract against
// the real module and PostgreSQL. All rows and the fixture ACTIVE gate roll back.
func TestMobileConsumersAcrossProjects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, "ALTER TABLE applications DROP CONSTRAINT applications_status_check")
	require.NoError(t, err)

	mobileKey, signer, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	originKey, originSigner, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	type consumer struct {
		project, platform, audience, token string
		client                             *http.Client
	}
	consumers := make([]consumer, 2)
	projects := make([]map[string]any, 2)
	for i, platform := range []string{"ANDROID", "IOS"} {
		var appID, userID int64
		var public string
		require.NoError(t, tx.QueryRow(ctx, "INSERT INTO applications(key,name,status) VALUES($1,'Consumer fixture','ACTIVE') RETURNING id, public_id::text", "consumer-"+strings.ToLower(platform)).Scan(&appID, &public))
		require.NoError(t, tx.QueryRow(ctx, "INSERT INTO users(application_id,external_id) VALUES($1,$2) RETURNING id", appID, "consumer-"+strings.ToLower(platform)).Scan(&userID))
		_, err := tx.Exec(ctx, "INSERT INTO mobile_identities(application_id,identity_namespace,subject,user_id) VALUES($1,'fixture-namespace','same-subject',$2)", appID, userID)
		require.NoError(t, err)
		audience := "consumer-" + platform
		raw, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
			"iss": "consumer-issuer", "aud": audience, "sub": "same-subject", "purpose": "access", "exp": time.Now().Add(time.Minute).Unix(),
		}).SignedString(signer)
		require.NoError(t, err)
		consumers[i] = consumer{public, platform, audience, raw, &http.Client{Timeout: 5 * time.Second}}
		projects[i] = map[string]any{
			"app_id": public, "issuer": "consumer-issuer", "audience": audience, "purpose": "access", "namespace": "fixture-namespace",
			"verification_key": []byte(mobileKey), "origin_issuer": "consumer-broker", "origin_audience": "enrollment", "origin_key": []byte(originKey),
			"routes": []map[string]any{{"platform": platform, "address_type": installation.FCMRegistrationToken, "provider_namespace": "shared-consumer-project", "provider_route_key": "consumer-route"}},
		}
	}
	cfg, err := json.Marshal(map[string]any{
		"current_key_version": 1, "fingerprint_key": bytes.Repeat([]byte{2}, 32),
		"encryption_keys": []map[string]any{{"version": 1, "key": bytes.Repeat([]byte{1}, 32)}}, "projects": projects,
	})
	require.NoError(t, err)
	runtime, err := installation.LoadRuntime(bytes.NewReader(cfg), sqlc.New(tx), installationLifecycleFixture{tx})
	require.NoError(t, err)
	mux := http.NewServeMux()
	applications.RegisterMobileLifecycle(mux, runtime)
	server := httptest.NewServer(mux)
	defer server.Close()
	defer consumers[0].client.CloseIdleConnections()
	defer consumers[1].client.CloseIdleConnections()

	request := func(c consumer, project, suffix string, body map[string]string, status int) map[string]string {
		t.Helper()
		payload, err := json.Marshal(body)
		require.NoError(t, err)
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/v1/apps/"+project+"/mobile/installations"+suffix, bytes.NewReader(payload))
		require.NoError(t, err)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+c.token)
		response, err := c.client.Do(r)
		require.NoError(t, err)
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, status, response.StatusCode, string(data))
		require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
		var envelope struct {
			Data map[string]string `json:"data"`
		}
		require.NoError(t, json.Unmarshal(data, &envelope))
		return envelope.Data
	}
	origin := func(c consumer, nonce string) string {
		t.Helper()
		now := time.Now()
		digest := sha256.Sum256([]byte(c.token))
		raw, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
			"iss": "consumer-broker", "aud": "enrollment", "sub": "same-subject", "purpose": "mobile_installation_enrollment",
			"app_id": c.project, "namespace": "fixture-namespace", "credential_digest": base64.RawURLEncoding.EncodeToString(digest[:]),
			"jti": nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(),
		}).SignedString(originSigner)
		require.NoError(t, err)
		return raw
	}
	a, b := consumers[0], consumers[1]
	bodyA := map[string]string{"platform": a.platform, "address_type": installation.FCMRegistrationToken, "address": "consumer-address-a", "origin": origin(a, "enroll-a")}
	stateA := request(a, a.project, "", bodyA, 201)
	require.NotEmpty(t, stateA["proof"])
	bodyB := map[string]string{"platform": b.platform, "address_type": installation.FCMRegistrationToken, "address": "consumer-address-b", "origin": bodyA["origin"]}
	request(b, b.project, "", bodyB, 401)
	bodyB["origin"] = origin(b, "conflict-b")
	bodyB["address"] = bodyA["address"]
	request(b, b.project, "", bodyB, 409)
	bodyB["origin"] = origin(b, "enroll-b")
	bodyB["address"] = "consumer-address-b"
	stateB := request(b, b.project, "", bodyB, 201)
	require.NotEqual(t, stateA["installation_id"], stateB["installation_id"])
	refresh := map[string]string{"proof": stateA["proof"], "version": "1", "platform": b.platform, "address_type": installation.FCMRegistrationToken, "address": bodyB["address"]}
	request(b, b.project, "/"+stateA["installation_id"]+"/refresh", refresh, 404)
	request(a, b.project, "/"+stateA["installation_id"]+"/refresh", refresh, 401)
	request(a, a.project, "/"+stateA["installation_id"]+"/logout", map[string]string{"proof": stateA["proof"], "version": "1"}, 200)
	refresh["proof"] = stateB["proof"]
	stateB = request(b, b.project, "/"+stateB["installation_id"]+"/refresh", refresh, 200)
	require.Equal(t, "1", stateB["version"])
	require.NotContains(t, stateB, "proof")
}
