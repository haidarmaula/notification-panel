package middleware

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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

func TestCORSMiddlewareMountedAPIPreflights(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			called := false
			handler := NewCORSMiddleware([]string{"https://admin.example.test"})(func(http.ResponseWriter, *http.Request) { called = true })
			r := httptest.NewRequest(http.MethodOptions, "/api/v1/staff", nil)
			r.Header.Set("Origin", "https://admin.example.test")
			r.Header.Set("Access-Control-Request-Method", method)
			r.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, X-API-Key, iF-mAtCh, IF-NONE-MATCH, Idempotency-Key")
			w := httptest.NewRecorder()
			handler(w, r)
			if w.Code != http.StatusNoContent || called {
				t.Fatalf("preflight status = %d, next called = %t", w.Code, called)
			}
			methods := strings.Split(w.Header().Get("Access-Control-Allow-Methods"), ", ")
			if !slices.Contains(methods, method) || slices.Contains(methods, "*") {
				t.Fatalf("allowed methods = %q", methods)
			}
			headers := strings.Split(strings.ToLower(w.Header().Get("Access-Control-Allow-Headers")), ", ")
			for header := range corsHeaders {
				if !slices.Contains(headers, header) {
					t.Errorf("allowed headers %q omit %q", headers, header)
				}
			}
			if slices.Contains(headers, "*") || w.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("wildcard headers or credentialed CORS unexpectedly enabled")
			}
		})
	}
}

func TestCORSMiddlewareRejectsPreflightWithEmptyConfiguration(t *testing.T) {
	called := false
	handler := NewCORSMiddleware(nil)(func(http.ResponseWriter, *http.Request) { called = true })
	r := httptest.NewRequest(http.MethodOptions, "/api/v1/staff", nil)
	r.Header.Set("Origin", "https://admin.example.test")
	r.Header.Set("Access-Control-Request-Method", http.MethodPut)
	r.Header.Set("Access-Control-Request-Headers", "If-Match, Idempotency-Key")
	w := httptest.NewRecorder()
	handler(w, r)
	if w.Code != http.StatusForbidden || called {
		t.Fatalf("preflight status = %d, next called = %t", w.Code, called)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Access-Control-Expose-Headers") != "" {
		t.Fatalf("empty configuration enabled browser access: %v", w.Header())
	}
}

func TestCORSMiddlewareRejectsUnsupportedPreflights(t *testing.T) {
	for _, tc := range []struct{ name, method, headers string }{
		{name: "unlisted method", method: http.MethodTrace, headers: "If-Match"},
		{name: "unlisted header with allowed headers", method: http.MethodPut, headers: "If-Match, Idempotency-Key, X-Unlisted"},
		{name: "cookie header", method: http.MethodPut, headers: "Cookie"},
		{name: "wildcard header", method: http.MethodPut, headers: "*"},
		{name: "near match header", method: http.MethodPut, headers: "If-Match-Other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := NewCORSMiddleware([]string{"https://admin.example.test"})(func(http.ResponseWriter, *http.Request) { called = true })
			r := httptest.NewRequest(http.MethodOptions, "/api/v1/staff", nil)
			r.Header.Set("Origin", "https://admin.example.test")
			r.Header.Set("Access-Control-Request-Method", tc.method)
			r.Header.Set("Access-Control-Request-Headers", tc.headers)
			w := httptest.NewRecorder()
			handler(w, r)
			if w.Code != http.StatusForbidden || called {
				t.Fatalf("preflight status = %d, next called = %t", w.Code, called)
			}
			if w.Header().Get("Access-Control-Allow-Methods") != "" || w.Header().Get("Access-Control-Allow-Headers") != "" {
				t.Fatal("rejected preflight advertised allowed methods or headers")
			}
		})
	}
}

func TestCORSMiddlewareETagExposureRequiresExactOrigin(t *testing.T) {
	for _, tc := range []struct {
		name    string
		origins []string
		origin  string
		allowed bool
	}{
		{name: "allowed", origins: []string{"https://admin.example.test"}, origin: "https://admin.example.test", allowed: true},
		{name: "disallowed", origins: []string{"https://admin.example.test"}, origin: "https://other.example.test"},
		{name: "origin suffix", origins: []string{"https://admin.example.test"}, origin: "https://admin.example.test.evil.invalid"},
		{name: "empty configuration", origin: "https://admin.example.test"},
		{name: "no origin", origins: []string{"https://admin.example.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, status := range []int{http.StatusOK, http.StatusNotModified, http.StatusPreconditionFailed} {
				r := httptest.NewRequest(http.MethodPut, "/api/v1/staff", nil)
				r.Header.Set("Origin", tc.origin)
				r.Header.Set("If-Match", `"revision-1"`)
				w := httptest.NewRecorder()
				called := false
				handler := NewCORSMiddleware(tc.origins)(func(w http.ResponseWriter, r *http.Request) {
					called = true
					if r.Header.Get("If-Match") != `"revision-1"` {
						t.Error("conditional request header was not preserved")
					}
					w.Header().Set("ETag", `"revision-2"`)
					w.WriteHeader(status)
				})
				handler(w, r)
				if !called || w.Code != status || w.Header().Get("ETag") != `"revision-2"` {
					t.Fatalf("actual response changed: called = %t, status = %d, headers = %v", called, w.Code, w.Header())
				}
				wantOrigin, wantExpose := "", ""
				if tc.allowed {
					wantOrigin, wantExpose = tc.origin, "ETag"
				}
				if w.Header().Get("Access-Control-Allow-Origin") != wantOrigin || w.Header().Get("Access-Control-Expose-Headers") != wantExpose {
					t.Fatalf("unexpected CORS response headers: %v", w.Header())
				}
				if w.Header().Get("Access-Control-Allow-Credentials") != "" {
					t.Fatal("credentialed CORS unexpectedly enabled")
				}
			}
		})
	}
}
