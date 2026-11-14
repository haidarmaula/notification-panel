package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, upstream http.HandlerFunc) (*Server, *httptest.Server) {
	t.Helper()
	api := httptest.NewTLSServer(upstream)
	t.Cleanup(api.Close)
	s, err := New(Config{Origin: "http://127.0.0.1:5173", Upstream: api.URL, APIKey: "private-key", Development: true, MaxSessions: 10}, api.Client())
	if err != nil {
		t.Fatal(err)
	}
	bff := httptest.NewServer(s)
	t.Cleanup(bff.Close)
	return s, bff
}
func request(t *testing.T, server *httptest.Server, method, path, body string, cookie *http.Cookie, csrf string) *http.Response {
	t.Helper()
	r, _ := http.NewRequestWithContext(context.Background(), method, server.URL+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://127.0.0.1:5173")
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}
func anonymous(t *testing.T, bff *httptest.Server) (*http.Cookie, string) {
	t.Helper()
	r := request(t, bff, "GET", "/bff/session", "", nil, "")
	var out struct {
		CSRF string `json:"csrfToken"`
	}
	json.NewDecoder(r.Body).Decode(&out)
	if out.CSRF == "" || len(r.Cookies()) != 1 {
		t.Fatal("missing pre-login CSRF/session")
	}
	return r.Cookies()[0], out.CSRF
}
func signedIn(t *testing.T, bff *httptest.Server) (*http.Cookie, string) {
	t.Helper()
	old, csrf := anonymous(t, bff)
	r := request(t, bff, "POST", "/bff/login", `{"email":"staff@example.invalid","password":"test-only"}`, old, csrf)
	var out struct {
		CSRF string `json:"csrfToken"`
	}
	json.NewDecoder(r.Body).Decode(&out)
	if r.StatusCode != 200 || len(r.Cookies()) != 1 || r.Cookies()[0].Value == old.Value {
		t.Fatalf("login/session rotation failed: %d", r.StatusCode)
	}
	return r.Cookies()[0], out.CSRF
}
func loginAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"success":true,"data":{"access_token":"old-access","refresh_token":"refresh-one"}}`)
}
func TestLoginRejectsCSRFAndKeepsCredentialsPrivate(t *testing.T) {
	_, bff := fixture(t, loginAPI)
	cookie, csrf := anonymous(t, bff)
	if r := request(t, bff, "POST", "/bff/login", `{}`, cookie, ""); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	r := request(t, bff, "POST", "/bff/login", `{"email":"staff@example.invalid","password":"test-only"}`, cookie, csrf)
	var raw map[string]any
	json.NewDecoder(r.Body).Decode(&raw)
	text := fmt.Sprint(raw)
	if strings.Contains(text, "access") || strings.Contains(text, "refresh") || strings.Contains(text, "private-key") {
		t.Fatal("credentials exposed")
	}
	if !r.Cookies()[0].HttpOnly || r.Cookies()[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("insecure cookie")
	}
	foreign, _ := http.NewRequest("POST", bff.URL+"/bff/login", strings.NewReader(`{}`))
	foreign.Header.Set("Origin", "https://evil.invalid")
	foreign.AddCookie(cookie)
	foreign.Header.Set("X-CSRF-Token", csrf)
	response, err := bff.Client().Do(foreign)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal(response.StatusCode)
	}
}
func TestConcurrentRequestsRotateRefreshOnce(t *testing.T) {
	var refreshes atomic.Int32
	_, bff := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "private-key" {
			t.Error("missing upstream key")
		}
		switch r.URL.Path {
		case "/api/v1/auth/login":
			loginAPI(w, r)
		case "/api/v1/auth/refresh":
			refreshes.Add(1)
			fmt.Fprint(w, `{"data":{"access_token":"new-access","refresh_token":"refresh-two"}}`)
		default:
			if r.Header.Get("Authorization") == "Bearer old-access" {
				w.WriteHeader(401)
			} else {
				fmt.Fprint(w, `{"success":true,"data":{"items":[]}}`)
			}
		}
	})
	cookie, _ := signedIn(t, bff)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			r := request(t, bff, "GET", "/bff/api/apps", "", cookie, "")
			if r.StatusCode != 200 {
				t.Error(r.StatusCode)
			}
		})
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("rotated %d times", refreshes.Load())
	}
}
func TestUncertainRefreshInvalidatesSessionWithoutReplay(t *testing.T) {
	var refreshes atomic.Int32
	_, bff := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			loginAPI(w, r)
		case "/api/v1/auth/refresh":
			refreshes.Add(1)
			connection, _, _ := w.(http.Hijacker).Hijack()
			connection.Close()
		default:
			w.WriteHeader(401)
		}
	})
	cookie, _ := signedIn(t, bff)
	for range 2 {
		if r := request(t, bff, "GET", "/bff/api/apps", "", cookie, ""); r.StatusCode != 401 {
			t.Fatal(r.StatusCode)
		}
	}
	if refreshes.Load() != 1 {
		t.Fatal("refresh replayed")
	}
}
func TestLogoutAndExpiredSessionsCannotReachUpstream(t *testing.T) {
	s, bff := fixture(t, loginAPI)
	cookie, csrf := signedIn(t, bff)
	if r := request(t, bff, "POST", "/bff/logout", `{}`, cookie, csrf); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	if r := request(t, bff, "GET", "/bff/api/apps", "", cookie, ""); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
	cookie, _ = signedIn(t, bff)
	s.mu.Lock()
	s.sessions[cookie.Value].expires = time.Now().Add(-time.Hour)
	s.mu.Unlock()
	if r := request(t, bff, "GET", "/bff/api/apps", "", cookie, ""); r.StatusCode != 401 {
		t.Fatal(r.StatusCode)
	}
}
func TestRejectsUnlistedProxyRoutesAndWrongMethods(t *testing.T) {
	_, bff := fixture(t, loginAPI)
	cookie, csrf := signedIn(t, bff)
	for _, path := range []string{"/bff/api/auth/login", "/bff/api/admin/apps/11111111-1111-4111-8111-111111111111/activate", "/bff/api/apps/not-a-uuid/campaigns"} {
		if r := request(t, bff, "GET", path, "", cookie, ""); r.StatusCode != 404 {
			t.Fatal(path, r.StatusCode)
		}
	}
	if r := request(t, bff, "DELETE", "/bff/api/apps", "", cookie, csrf); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
}

func TestAnonymousFloodDoesNotConsumeAuthenticatedCapacity(t *testing.T) {
	s, bff := fixture(t, loginAPI)
	cookie, _ := signedIn(t, bff)
	for range 30 {
		anonymous(t, bff)
	}
	s.mu.Lock()
	count := len(s.sessions)
	s.mu.Unlock()
	if count > 11 {
		t.Fatalf("unbounded sessions: %d", count)
	}
	if response := request(t, bff, "GET", "/bff/session", "", cookie, ""); response.StatusCode != 200 {
		t.Fatal("authenticated session evicted")
	}
	fresh, _ := signedIn(t, bff)
	if fresh.Value == cookie.Value {
		t.Fatal("session identity reused")
	}
}

func TestRedirectCannotForwardCredentialsToAnotherOrigin(t *testing.T) {
	var received atomic.Int32
	foreign := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); w.WriteHeader(200) }))
	defer foreign.Close()
	_, bff := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL, http.StatusTemporaryRedirect)
	})
	cookie, csrf := anonymous(t, bff)
	response := request(t, bff, "POST", "/bff/login", `{"email":"staff@example.invalid","password":"test-only"}`, cookie, csrf)
	if response.StatusCode != 502 || received.Load() != 0 {
		t.Fatal("upstream redirect was followed")
	}
}
func TestSessionCookieSecureOutsideLoopbackDevelopment(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(loginAPI))
	defer upstream.Close()
	server, err := New(Config{Origin: "https://dashboard.example.invalid", Upstream: upstream.URL, APIKey: "test-key"}, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest("GET", "/bff/session", nil))
	cookie := recorder.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.Name != "__Host-hello_session" {
		t.Fatal("missing secure cookie attributes")
	}
	if _, err := New(Config{Origin: "https://public.example.invalid", Upstream: upstream.URL, APIKey: "test-key", Development: true}, upstream.Client()); err == nil {
		t.Fatal("development accepted a non-loopback origin")
	}
}
