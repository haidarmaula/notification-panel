package provisioning

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const testIngressIdentity = "spiffe://hello.test/ingress"
const testReservationID = "33333333-3333-4333-8333-333333333333"

type verifierStub struct {
	err   error
	calls int
}

func (v *verifierStub) VerifyStage(context.Context, string, string, string, string, string, string) error {
	v.calls++
	return v.err
}

type storeStub struct {
	err   error
	calls int
}

func (s *storeStub) Stage(_ context.Context, appID, environment, configID, _, _ string, data []byte) (StageResult, error) {
	s.calls++
	if s.err != nil {
		return StageResult{}, s.err
	}
	return StageResult{Reference: configID, AppID: appID, Environment: environment, ConfigID: configID, Size: int64(len(data))}, nil
}

func TestServiceRequiresVerifiedPinnedIdentityAndReservation(t *testing.T) {
	verifier, store := &verifierStub{}, &storeStub{}
	service, err := NewService(testIngressIdentity, verifier, store)
	if err != nil {
		t.Fatal("NewService rejected valid configuration")
	}
	body := encodedRequest(t, validRequest())

	response := serveStage(service, body, nil, "application/json")
	if response.Code != http.StatusForbidden || verifier.calls != 0 || store.calls != 0 {
		t.Fatal("service accepted a request without verified TLS identity")
	}
	response = serveStage(service, body, tlsState("spiffe://hello.test/other"), "application/json")
	if response.Code != http.StatusForbidden || store.calls != 0 {
		t.Fatal("service accepted the wrong leaf URI identity")
	}

	verifier.err = errors.New("database failure: secret-bearing detail")
	response = serveStage(service, body, tlsState(testIngressIdentity), "application/json")
	if response.Code != http.StatusForbidden || store.calls != 0 || strings.Contains(response.Body.String(), "secret-bearing") {
		t.Fatal("reservation failure was not denied safely before storage")
	}
	verifier.err = nil
	response = serveStage(service, body, tlsState(testIngressIdentity), "application/json")
	if response.Code != http.StatusCreated || store.calls != 1 || verifier.calls != 2 {
		t.Fatalf("valid stage failed: status=%d verifier=%d store=%d", response.Code, verifier.calls, store.calls)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("stage response is cacheable")
	}
	var result StageResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Reference != testConfigID || result.Size == 0 {
		t.Fatal("stage response did not contain safe metadata")
	}
}

func TestServiceBoundsAndRejectsMalformedRequests(t *testing.T) {
	verifier, store := &verifierStub{}, &storeStub{}
	service, err := NewService(testIngressIdentity, verifier, store)
	if err != nil {
		t.Fatal("NewService rejected valid configuration")
	}
	cases := []struct {
		name, body, contentType string
		status                  int
	}{
		{"invalid JSON", "{", "application/json", http.StatusBadRequest},
		{"duplicate field", `{"application_id":"x","application_id":"x"}`, "application/json", http.StatusBadRequest},
		{"wrong media type", string(encodedRequest(t, validRequest())), "text/plain", http.StatusBadRequest},
		{"too large", strings.Repeat("x", maxStageRequestSize+1), "application/json", http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := serveStage(service, []byte(tc.body), tlsState(testIngressIdentity), tc.contentType)
			if response.Code != tc.status {
				t.Fatalf("status=%d, want %d", response.Code, tc.status)
			}
		})
	}
	if verifier.calls != 0 || store.calls != 0 {
		t.Fatal("malformed request reached downstream dependencies")
	}
}

func TestServiceMapsStoreFailureAndExposesOnlyStageRoute(t *testing.T) {
	verifier, store := &verifierStub{}, &storeStub{err: errors.New("private key details")}
	service, err := NewService(testIngressIdentity, verifier, store)
	if err != nil {
		t.Fatal("NewService rejected valid configuration")
	}
	response := serveStage(service, encodedRequest(t, validRequest()), tlsState(testIngressIdentity), "application/json")
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "private key") {
		t.Fatal("store error was exposed")
	}
	for _, pair := range []struct{ method, path string }{{http.MethodDelete, "/v1/stages"}, {http.MethodPost, "/v1/abort"}} {
		request := httptest.NewRequest(pair.method, pair.path, nil)
		request.TLS = tlsState(testIngressIdentity)
		response := httptest.NewRecorder()
		service.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatal("service exposed an unsupported operation")
		}
	}
}

func TestNewServiceRequiresIdentityAndDependencies(t *testing.T) {
	store, verifier := &storeStub{}, &verifierStub{}
	if _, err := NewService("", verifier, store); err == nil {
		t.Fatal("accepted empty URI identity")
	}
	if _, err := NewService(testIngressIdentity, nil, store); err == nil {
		t.Fatal("accepted missing verifier")
	}
	if _, err := NewService(testIngressIdentity, verifier, nil); err == nil {
		t.Fatal("accepted missing store")
	}
}

func validRequest() StageRequest {
	return StageRequest{AppID: testAppID, ReservationID: testReservationID, ConfigID: testConfigID, Environment: "staging",
		TargetProject: "fixture-project", ExpectedEmail: "fixture@fixture-project.iam.gserviceaccount.com", ServiceAccount: json.RawMessage(`{"synthetic":true}`)}
}

func encodedRequest(t *testing.T, request StageRequest) []byte {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal("cannot encode test request")
	}
	return data
}

func tlsState(identity string) *tls.ConnectionState {
	uri, _ := url.Parse(identity)
	leaf := &x509.Certificate{URIs: []*url.URL{uri}}
	return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}}
}

func serveStage(service http.Handler, body []byte, state *tls.ConnectionState, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/stages", strings.NewReader(string(body)))
	request.TLS = state
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	return response
}
