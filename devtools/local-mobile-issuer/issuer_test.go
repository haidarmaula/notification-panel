package main

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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
	"hello/internal/installation"
)

func testConfig(t *testing.T) issuerConfig {
	t.Helper()
	_, mobile, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("generate mobile signing key")
	}
	_, origin, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("generate origin signing key")
	}
	return issuerConfig{
		AppID: "8b60b6c6-7ea7-4eab-9ac7-7d10bb8efde1", Issuer: "mobile-test",
		Audience: "mobile-client", Purpose: "mobile-installation", Namespace: "test-users",
		Subject: "device-test-subject", MobilePrivateKey: mobile, OriginIssuer: "origin-test",
		OriginAudience: "origin-client", OriginPrivateKey: origin,
	}
}

func TestCreateFilesWritesMatchingPrivateIssuerAndMobilePolicy(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "mobile-issuer")
	if err := createFiles(directory, "8b60b6c6-7ea7-4eab-9ac7-7d10bb8efde1", "test-users", "device-test-subject", "hello-local-fcm", "android-fid"); err != nil {
		t.Fatal("create local files")
	}
	issuerPath := filepath.Join(directory, "issuer.json")
	cfg, err := readConfig(issuerPath)
	if err != nil {
		t.Fatal("read generated issuer file")
	}
	issuerInfo, err := os.Stat(issuerPath)
	if err != nil {
		t.Fatal("stat issuer file")
	}
	if issuerInfo.Mode().Perm() != 0600 {
		t.Fatalf("issuer file mode = %04o, want 0600", issuerInfo.Mode().Perm())
	}
	data, err := os.ReadFile(filepath.Join(directory, "mobile-policy.json"))
	if err != nil {
		t.Fatal("read generated policy")
	}
	var policy mobilePolicyFile
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal("decode generated policy")
	}
	if len(policy.Projects) != 1 || policy.Projects[0].AppID != cfg.AppID ||
		string(policy.Projects[0].VerificationKey) != string(ed25519.PrivateKey(cfg.MobilePrivateKey).Public().(ed25519.PublicKey)) ||
		string(policy.Projects[0].OriginKey) != string(ed25519.PrivateKey(cfg.OriginPrivateKey).Public().(ed25519.PublicKey)) {
		t.Fatal("policy issuer keys or app identity do not match generated issuer")
	}
	runtime, err := installation.LoadRuntime(bytes.NewReader(data), testRuntimeStore{}, testTransactor{})
	if err != nil {
		t.Fatalf("generated policy is rejected by the backend: %v", err)
	}
	var projectID pgtype.UUID
	if err := projectID.Scan(cfg.AppID); err != nil {
		t.Fatal("parse test app id")
	}
	route := policy.Projects[0].Routes[0]
	if route.Platform != "ANDROID" || route.AddressType != "FCM_FID" ||
		route.ProviderNamespace != "hello-local-fcm" || route.ProviderRouteKey != "android-fid" {
		t.Fatalf("unexpected generated route: %+v", route)
	}
	if actual, ok := runtime.Route(projectID, route.Platform, route.AddressType); !ok ||
		actual.Namespace != route.ProviderNamespace || actual.Key != route.ProviderRouteKey {
		t.Fatalf("backend route = %+v, %v; want configured route", actual, ok)
	}
	if err := createFiles(directory, cfg.AppID, cfg.Namespace, cfg.Subject, route.ProviderNamespace, route.ProviderRouteKey); err == nil {
		t.Fatal("refusing to overwrite local key directory")
	}
}

type testRuntimeStore struct{}

func (testRuntimeStore) GetApplicationByPublicID(context.Context, pgtype.UUID) (sqlc.Application, error) {
	return sqlc.Application{}, nil
}

func (testRuntimeStore) ResolveMobileIdentity(context.Context, sqlc.ResolveMobileIdentityParams) (int64, error) {
	return 0, nil
}

func (testRuntimeStore) ConsumeMobileOrigin(context.Context, sqlc.ConsumeMobileOriginParams) (int64, error) {
	return 0, nil
}

type testTransactor struct{}

func (testTransactor) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return nil, nil
}

func TestSessionIssuesBoundCredentialAndFreshOneUseOriginAssertions(t *testing.T) {
	cfg := testConfig(t)
	api := &issuerServer{cfg: cfg, pairingCode: "pair-once", pairingExpires: time.Now().Add(time.Minute)}

	sessionBody := `{"app_id":"` + cfg.AppID + `","pairing_code":"pair-once"}`
	response := request(t, api.handler(), "/v1/session", "", sessionBody)
	if response.Code != http.StatusOK {
		t.Fatalf("session status = %d; body=%s", response.Code, response.Body.String())
	}
	var session apiEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal("decode session response")
	}
	credential := session.Data.(map[string]any)["credential"].(string)
	var claims mobileClaims
	parsed, err := jwt.ParseWithClaims(credential, &claims, func(_ *jwt.Token) (any, error) {
		return ed25519.PrivateKey(cfg.MobilePrivateKey).Public(), nil
	}, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(cfg.Issuer), jwt.WithAudience(cfg.Audience), jwt.WithExpirationRequired())
	if err != nil || parsed == nil || !parsed.Valid || claims.Subject != cfg.Subject || claims.Purpose != cfg.Purpose {
		t.Fatalf("mobile credential failed verification: %v", err)
	}

	originBody := `{"app_id":"` + cfg.AppID + `"}`
	originResponse := request(t, api.handler(), "/v1/origin", credential, originBody)
	if originResponse.Code != http.StatusOK {
		t.Fatalf("origin status = %d; body=%s", originResponse.Code, originResponse.Body.String())
	}
	var originEnvelope apiEnvelope
	if err := json.Unmarshal(originResponse.Body.Bytes(), &originEnvelope); err != nil {
		t.Fatal("decode origin response")
	}
	origin := originEnvelope.Data.(map[string]any)["origin"].(string)
	var originToken originClaims
	originParsed, err := jwt.ParseWithClaims(origin, &originToken, func(_ *jwt.Token) (any, error) {
		return ed25519.PrivateKey(cfg.OriginPrivateKey).Public(), nil
	}, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(cfg.OriginIssuer), jwt.WithAudience(cfg.OriginAudience), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || originParsed == nil || !originParsed.Valid || originToken.AppID != cfg.AppID ||
		originToken.Namespace != cfg.Namespace || originToken.Subject != cfg.Subject || originToken.Purpose != originPurpose {
		t.Fatalf("origin assertion failed verification: %v", err)
	}
	digest := sha256.Sum256([]byte(credential))
	if originToken.CredentialDigest != base64.RawURLEncoding.EncodeToString(digest[:]) {
		t.Fatal("origin assertion is not bound to the exact mobile credential")
	}
	if originToken.ExpiresAt.Time.Sub(originToken.IssuedAt.Time) != originTokenLifetime {
		t.Fatal("origin assertion lifetime does not match the bounded contract")
	}
	firstID := originToken.ID
	for attempt := 1; attempt < maxOriginAssertions; attempt++ {
		freshResponse := request(t, api.handler(), "/v1/origin", credential, originBody)
		if freshResponse.Code != http.StatusOK {
			t.Fatalf("fresh origin attempt %d status = %d; body=%s", attempt+1, freshResponse.Code, freshResponse.Body.String())
		}
		var freshEnvelope apiEnvelope
		if err := json.Unmarshal(freshResponse.Body.Bytes(), &freshEnvelope); err != nil {
			t.Fatal("decode fresh origin response")
		}
		freshOrigin := freshEnvelope.Data.(map[string]any)["origin"].(string)
		var freshClaims originClaims
		freshParsed, err := jwt.ParseWithClaims(freshOrigin, &freshClaims, func(_ *jwt.Token) (any, error) {
			return ed25519.PrivateKey(cfg.OriginPrivateKey).Public(), nil
		}, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(cfg.OriginIssuer), jwt.WithAudience(cfg.OriginAudience))
		if err != nil || freshParsed == nil || !freshParsed.Valid || freshClaims.ID == firstID {
			t.Fatalf("fresh origin assertion invalid or reused nonce: %v", err)
		}
		firstID = freshClaims.ID
	}
	if capped := request(t, api.handler(), "/v1/origin", credential, originBody); capped.Code != http.StatusUnauthorized {
		t.Fatalf("origin issuance past cap status = %d, want 401", capped.Code)
	}
	if replay := request(t, api.handler(), "/v1/session", "", sessionBody); replay.Code != http.StatusUnauthorized {
		t.Fatalf("pairing code replay status = %d, want 401", replay.Code)
	}
}

func TestReadConfigRejectsInconsistentEd25519PrivateKey(t *testing.T) {
	cfg := testConfig(t)
	cfg.MobilePrivateKey[len(cfg.MobilePrivateKey)-1] ^= 0xff
	if validConfig(cfg) {
		t.Fatal("validConfig accepted private key with mismatched embedded public key")
	}
}

func TestSessionRejectsDuplicateJSONAndInvalidPairingCode(t *testing.T) {
	cfg := testConfig(t)
	api := &issuerServer{cfg: cfg, pairingCode: "pair-once", pairingExpires: time.Now().Add(time.Minute)}

	duplicate := `{"app_id":"` + cfg.AppID + `","app_id":"` + cfg.AppID + `","pairing_code":"pair-once"}`
	if response := request(t, api.handler(), "/v1/session", "", duplicate); response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate-field status = %d, want 400", response.Code)
	}
	wrongCode := `{"app_id":"` + cfg.AppID + `","pairing_code":"wrong"}`
	if response := request(t, api.handler(), "/v1/session", "", wrongCode); response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-code status = %d, want 401", response.Code)
	}
}

func TestReadConfigRejectsBroadFilePermissions(t *testing.T) {
	cfg := testConfig(t)
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal("marshal config")
	}
	path := filepath.Join(t.TempDir(), "issuer.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal("write config")
	}
	if _, err := readConfig(path); err == nil {
		t.Fatal("readConfig accepted group- and other-readable signing keys")
	}
}

func TestLoopbackAddressRejectsNetworkExposure(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8081", "[::1]:8081"} {
		if !loopbackAddress(address) {
			t.Errorf("loopbackAddress(%q) = false", address)
		}
	}
	for _, address := range []string{"0.0.0.0:8081", "192.168.1.2:8081", "localhost:8081", ":8081"} {
		if loopbackAddress(address) {
			t.Errorf("loopbackAddress(%q) = true", address)
		}
	}
}

func request(t *testing.T, handler http.Handler, path, credential, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	handler.ServeHTTP(recorder, req)
	return recorder
}
