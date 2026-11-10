package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSMiddlewareAllowsConfiguredOriginAndPreflight(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	})
	handler := http.HandlerFunc(NewCORSMiddleware([]string{"https://admin.example.test"})(next.ServeHTTP))

	actual := httptest.NewRequest(http.MethodGet, "/api/v1/staff", nil)
	actual.Header.Set("Origin", "https://admin.example.test")
	actualResponse := httptest.NewRecorder()
	handler.ServeHTTP(actualResponse, actual)
	if actualResponse.Code != http.StatusAccepted || !called {
		t.Fatalf("actual request status = %d, next called = %t", actualResponse.Code, called)
	}
	if got := actualResponse.Header().Get("Access-Control-Allow-Origin"); got != "https://admin.example.test" {
		t.Fatalf("allowed origin = %q", got)
	}
	if got := actualResponse.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("credentialed CORS unexpectedly enabled: %q", got)
	}

	called = false
	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/staff", nil)
	preflight.Header.Set("Origin", "https://admin.example.test")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "authorization, content-type, x-api-key")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent || called {
		t.Fatalf("preflight status = %d, next called = %t", preflightResponse.Code, called)
	}
	if got := preflightResponse.Header().Get("Access-Control-Allow-Origin"); got != "https://admin.example.test" {
		t.Fatalf("preflight allowed origin = %q", got)
	}
}

func TestCORSMiddlewareRejectsUnlistedOriginAndHeaders(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	handler := http.HandlerFunc(NewCORSMiddleware([]string{"https://admin.example.test"})(next.ServeHTTP))

	for _, test := range []struct {
		name    string
		origin  string
		headers string
	}{
		{name: "unlisted origin", origin: "https://other.example.test", headers: "authorization"},
		{name: "unlisted header", origin: "https://admin.example.test", headers: "authorization, x-unlisted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			called = false
			request := httptest.NewRequest(http.MethodOptions, "/api/v1/staff", nil)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Access-Control-Request-Method", http.MethodPost)
			request.Header.Set("Access-Control-Request-Headers", test.headers)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || called {
				t.Fatalf("status = %d, next called = %t", response.Code, called)
			}
		})
	}
}

func TestCORSMiddlewarePassesRequestsWithoutOrigin(t *testing.T) {
	called := false
	handler := http.HandlerFunc(NewCORSMiddleware(nil)(func(http.ResponseWriter, *http.Request) { called = true }))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if !called || response.Code != http.StatusOK {
		t.Fatalf("next called = %t, status = %d", called, response.Code)
	}
}
