package fcm

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/auth"
)

func fileFixture(t *testing.T) ([]byte, FileCredentialPolicy, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "fixture-project", "private_key_id": "synthetic-kid",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "fixture@fixture-project.iam.gserviceaccount.com", "token_uri": oauthEndpoint,
		"auth_uri": "https://accounts.google.com/o/oauth2/auth", "client_id": "12345",
		"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
		"client_x509_cert_url":        "https://www.googleapis.com/robot/v1/metadata/x509/fixture",
		"universe_domain":             "googleapis.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	return data, FileCredentialPolicy{Path: "/etc/hello/synthetic-secret.json", CredentialProjectID: "fixture-project", ServiceAccountEmail: "fixture@fixture-project.iam.gserviceaccount.com"}, key
}
func TestServiceAccountIdentityAndKeyValidation(t *testing.T) {
	data, policy, _ := fileFixture(t)
	if _, err := parseServiceAccount(data, "fixture-project", policy); err != nil {
		t.Fatal("valid key rejected")
	}
	for _, name := range []string{"type", "project_id", "client_email", "token_uri", "private_key_id", "private_key", "universe_domain", "unknown", "null", "missing", "duplicate", "case", "trailing", "oversized", "prefix"} {
		t.Run(name, func(t *testing.T) {
			var fields map[string]any
			if json.Unmarshal(data, &fields) != nil {
				t.Fatal("fixture decode")
			}
			switch name {
			case "unknown":
				fields["credential_source"] = map[string]any{"executable": "untrusted"}
			case "null":
				fields["client_email"] = nil
			case "missing":
				delete(fields, "private_key_id")
			case "case":
				fields["Client_Email"] = fields["client_email"]
				delete(fields, "client_email")
			case "private_key_id":
				fields["private_key_id"] = "invalid\nkey"
			case "prefix":
				fields["private_key"] = "ignored prefix\n" + fields["private_key"].(string)
			default:
				fields[name] = "untrusted"
			}
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "duplicate":
				raw = append([]byte(`{"type":"service_account",`), data[1:]...)
			case "trailing":
				raw = append(data, []byte(` {}`)...)
			case "oversized":
				raw = []byte(strings.Repeat("x", 64*1024+1))
			}
			if _, err := parseServiceAccount(raw, "fixture-project", policy); err != ErrConfiguration {
				t.Fatal("unsafe credential accepted")
			}
		})
	}
	t.Run("cross project requires approval", func(t *testing.T) {
		if _, err := parseServiceAccount(data, "other-project", policy); err != ErrConfiguration {
			t.Fatal("implicit cross-project grant")
		}
		policy.CrossProjectApproved = true
		if _, err := parseServiceAccount(data, "other-project", policy); err != nil {
			t.Fatal("explicit cross-project policy rejected")
		}
	})
	t.Run("protected file required", func(t *testing.T) {
		config, _ := transportFixture(t)
		policy.Path = t.TempDir() + "/key.json"
		if c, err := NewFile(context.Background(), config, policy); c != nil || err != ErrConfiguration {
			t.Fatal("untrusted path accepted")
		}
	})
}

func assertOAuthAssertion(t *testing.T, r *http.Request, key *rsa.PublicKey) {
	t.Helper()
	if r.URL.String() != oauthEndpoint || r.Method != http.MethodPost || r.GetBody != nil || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Error("wrong OAuth boundary")
		return
	}
	if r.ParseForm() != nil || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Error("wrong OAuth grant")
		return
	}
	parts := strings.Split(r.Form.Get("assertion"), ".")
	if len(parts) != 3 {
		t.Error("wrong assertion encoding")
		return
	}
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Error("wrong claims encoding")
		return
	}
	var body struct {
		Iss, Aud, Scope, Sub string
		Iat, Exp             int64
	}
	if json.Unmarshal(claims, &body) != nil || body.Iss != "fixture@fixture-project.iam.gserviceaccount.com" || body.Aud != oauthEndpoint || body.Scope != messagingScope || body.Sub != "" || body.Exp <= body.Iat || body.Exp-body.Iat > 3600 {
		t.Error("wrong assertion scope")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig) != nil {
		t.Error("assertion signature invalid")
	}
}
func TestFileSourceLibraryAssertionAndSynchronousCache(t *testing.T) {
	data, policy, key := fileFixture(t)
	config, _ := transportFixture(t)
	// SDK debug environment must not enable assertion/key/response logging.
	t.Setenv("GOOGLE_SDK_GO_LOGGING_LEVEL", "debug")
	var calls atomic.Int32
	source, cleanup, err := fileSource(config, policy, data, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		assertOAuthAssertion(t, r, &key.PublicKey)
		return response(200, `{"access_token":"synthetic_access_token","token_type":"Bearer","expires_in":3600}`), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			token, err := source.Token(context.Background())
			if err != nil || token.Value != "synthetic_access_token" {
				t.Error("token exchange failed")
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent refresh not coalesced")
	}
	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	token.Value = "caller-mutated"
	token.Expiry = time.Time{}
	token, err = source.Token(context.Background())
	if err != nil || token.Value != "synthetic_access_token" || calls.Load() != 1 {
		t.Fatal("returned token mutated cache")
	}
	source.cached.Expiry = time.Now().Add(30 * time.Second)
	if _, err := source.Token(context.Background()); err != nil || calls.Load() != 2 {
		t.Fatal("near-expiry token was not synchronously refreshed")
	}
}
func TestFileSourceWaitersCancelAndRefreshJoins(t *testing.T) {
	data, policy, _ := fileFixture(t)
	config, _ := transportFixture(t)
	started, joined := make(chan struct{}), make(chan struct{})
	source, cleanup, err := fileSource(config, policy, data, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(joined)
		return nil, r.Context().Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	done := make(chan error, 1)
	go func() { _, err := source.Token(first); done <- err }()
	<-started
	waiter, cancelWaiter := context.WithCancel(context.Background())
	cancelWaiter()
	if token, err := source.Token(waiter); token != nil || err != ErrCredentials {
		t.Fatal("cancelled waiter blocked or received token")
	}
	cancelFirst()
	if err := <-done; err != ErrCredentials {
		t.Fatal("raw refresh error escaped")
	}
	select {
	case <-joined:
	default:
		t.Fatal("refresh callback abandoned")
	}
}
func TestOAuthResponsesAreBoundedAndSafe(t *testing.T) {
	data, policy, _ := fileFixture(t)
	config, _ := transportFixture(t)
	cases := []struct {
		name, body string
		status     int
	}{
		{"huge expiry", `{"access_token":"synthetic","token_type":"Bearer","expires_in":9223372036854775807}`, 200},
		{"expired", `{"access_token":"synthetic","token_type":"Bearer","expires_in":0}`, 200},
		{"id token", `{"access_token":"synthetic","token_type":"Bearer","expires_in":3600,"id_token":"opaque"}`, 200},
		{"wrong type", `{"access_token":"synthetic","token_type":"Basic","expires_in":3600}`, 200},
		{"empty", `{}`, 200}, {"oversized", strings.Repeat("sensitive", 3000), 200}, {"rejected", `{"error":"secret rejection"}`, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source, cleanup, err := fileSource(config, policy, data, roundTripFunc(func(*http.Request) (*http.Response, error) { return response(tc.status, tc.body), nil }))
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if token, err := source.Token(context.Background()); token != nil || !errors.Is(err, ErrCredentials) || err.Error() != ErrCredentials.Error() {
				t.Fatal("unsafe token/error returned")
			}
		})
	}
}

var _ auth.TokenProvider = (*fileTokenSource)(nil)

func TestRSAEncodingAndStrength(t *testing.T) {
	_, _, key := fileFixture(t)
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	if !validRSAKey(pkcs1) {
		t.Fatal("valid PKCS1 rejected")
	}
	if validRSAKey(pkcs1+pkcs1) || validRSAKey("prefix\n"+pkcs1) {
		t.Fatal("multiple/prefixed keys accepted")
	}
	weak, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if validRSAKey(string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(weak)}))) {
		t.Fatal("weak RSA accepted")
	}
}
