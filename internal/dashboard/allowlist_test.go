package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWorkspaceAllowlist(t *testing.T) {
	base := "/apps/11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		method, suffix string
		want           bool
	}{
		{"POST", "/segments", true}, {"PUT", "/segments/7", true}, {"DELETE", "/segments/7", true},
		{"GET", "/segments/7/members", true}, {"PUT", "/segments/7/members/42", true}, {"DELETE", "/segments/7/members/42", true},
		{"POST", "/segments/7/members/42", false}, {"GET", "/segments/7/members/42", false},
		{"PUT", "/segments/7/members", false}, {"PUT", "/segments/7/members/0", false},
		{"GET", "/users/42/devices", true}, {"PUT", "/users/42/devices", false}, {"GET", "/users/42/devices/1", false},
		{"POST", "/templates", true}, {"PUT", "/templates/1", true}, {"DELETE", "/templates/1", false},
		{"POST", "/templates/1/send", false}, {"PUT", "/users/42", false}, {"POST", "/dispatch/open", false},
		{"GET", "/staff", true}, {"POST", "/staff", true}, {"GET", "/staff/42", true},
		{"PATCH", "/staff/42", true}, {"PATCH", "/staff/42/status", true}, {"PATCH", "/staff/42/password", true},
		{"DELETE", "/staff/42", true}, {"GET", "/staff/0", false}, {"GET", "/staff/9223372036854775808", false},
		{"POST", "/staff/42", false}, {"PATCH", "/staff/42/other", false}, {"GET", "/staff/42/password", false},
		{"PATCH", "/profile/password", true}, {"GET", "/profile/password", false},
		{"GET", "/admin/apps", true}, {"POST", "/admin/apps", true},
		{"GET", "/admin/apps/11111111-1111-4111-8111-111111111111", true},
		{"PATCH", "/admin/apps/11111111-1111-4111-8111-111111111111", true},
		{"GET", "/admin/apps/11111111-1111-4111-8111-111111111111/staff-grants", true},
		{"GET", "/admin/apps/11111111-1111-4111-8111-111111111111/staff-grants/42", true},
		{"PUT", "/admin/apps/11111111-1111-4111-8111-111111111111/staff-grants/42", true},
		{"DELETE", "/admin/apps/11111111-1111-4111-8111-111111111111/staff-grants/42", true},
		{"POST", "/admin/apps/11111111-1111-4111-8111-111111111111/staff-grants", false},
		{"DELETE", "/admin/apps/11111111-1111-4111-8111-111111111111", false},
		{"GET", "/admin/apps/11111111-1111-4111-8111-111111111111/activate", false},
		{"GET", "/admin/apps/11111111-1111-4111-8111-111111111111/staff-grants/0", false},
		{"GET", "/admin/apps/11111111-1111-4111-8111-111111111111/staff-grants/9223372036854775808", false},
		{"GET", "/apps/11111111-1111-4111-8111-111111111111/uploads", true},
		{"GET", "/apps/11111111-1111-4111-8111-111111111111/uploads/42/rows", true},
		{"GET", "/apps/11111111-1111-4111-8111-111111111111/audit-logs", true},
		{"POST", "/apps/11111111-1111-4111-8111-111111111111/uploads", false},
		{"GET", "/apps/11111111-1111-4111-8111-111111111111/uploads/42", false},
		{"GET", "/apps/11111111-1111-4111-8111-111111111111/audit-logs/42", false},
	} {
		t.Run(tc.method+tc.suffix, func(t *testing.T) {
			path := base + tc.suffix
			if strings.HasPrefix(tc.suffix, "/staff") || tc.suffix == "/profile/password" || strings.HasPrefix(tc.suffix, "/admin/") || tc.suffix == "/admin/apps" || strings.HasPrefix(tc.suffix, "/apps/") {
				path = tc.suffix
			}
			if got := allowed(tc.method, path); got != tc.want {
				t.Fatalf("allowed = %v, want %v", got, tc.want)
			}
		})
	}
	if !allowed("PATCH", "/profile") {
		t.Fatal("profile update was removed")
	}
}

func TestCampaignAcceptance503ClassificationIsScoped(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/apps/11111111-1111-4111-8111-111111111111/campaigns/42/send", true},
		{"/apps/11111111-1111-4111-8111-111111111111/campaigns/42/schedule", true},
		{"/admin/apps/11111111-1111-4111-8111-111111111111", false},
		{"/profile/password", false},
		{"/apps/11111111-1111-4111-8111-111111111111/campaigns/42", false},
	} {
		if got := campaignAcceptancePath(tc.path); got != tc.want {
			t.Errorf("campaignAcceptancePath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestRegistryConditionalHeadersAndETagAreForwarded(t *testing.T) {
	app := "/admin/apps/11111111-1111-4111-8111-111111111111"
	grant := app + "/staff-grants/42"
	for _, tc := range []struct {
		method, path, requestHeader, requestValue string
	}{
		{http.MethodPatch, app, "If-Match", `"app:11111111-1111-4111-8111-111111111111:1"`},
		{http.MethodPut, grant, "If-None-Match", "*"},
		{http.MethodDelete, grant, "If-Match", `"grant:11111111-1111-4111-8111-111111111111:42:1"`},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			_, bff := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/auth/login" {
					loginAPI(w, r)
					return
				}
				if got := r.Header.Get(tc.requestHeader); got != tc.requestValue {
					t.Errorf("forwarded %s = %q, want %q", tc.requestHeader, got, tc.requestValue)
				}
				if r.Header.Get("If-Match") != "" && tc.requestHeader != "If-Match" || r.Header.Get("If-None-Match") != "" && tc.requestHeader != "If-None-Match" {
					t.Error("forwarded an unrelated conditional header")
				}
				w.Header().Set("ETag", `"actual-upstream-tag"`)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"success":true,"data":{}}`)
			})
			cookie, csrf := signedIn(t, bff)
			r, err := http.NewRequest(tc.method, bff.URL+"/bff/api"+tc.path, strings.NewReader(`{"name":"renamed"}`))
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Origin", "http://127.0.0.1:5173")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set(tc.requestHeader, tc.requestValue)
			r.AddCookie(cookie)
			r.Header.Set("X-CSRF-Token", csrf)
			response, err := bff.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK || response.Header.Get("ETag") != `"actual-upstream-tag"` {
				t.Fatalf("response status/ETag = %d/%q", response.StatusCode, response.Header.Get("ETag"))
			}
		})
	}
}

func TestSelfPasswordWrongCurrentPasswordDoesNotRefreshOrLogout(t *testing.T) {
	var refreshes, passwordCalls int
	_, bff := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			loginAPI(w, r)
		case "/api/v1/auth/refresh":
			refreshes++
			fmt.Fprint(w, `{"data":{"access_token":"fresh-access","refresh_token":"fresh-refresh"}}`)
		case "/api/v1/profile/password":
			passwordCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"success":false,"message":"current password is incorrect"}`)
		default:
			t.Errorf("unexpected upstream route %s", r.URL.Path)
		}
	})
	cookie, csrf := signedIn(t, bff)
	response := request(t, bff, http.MethodPatch, "/bff/api/profile/password", `{"current_password":"wrong","new_password":"replacement"}`, cookie, csrf)
	if response.StatusCode != http.StatusUnprocessableEntity || refreshes != 0 || passwordCalls != 1 {
		t.Fatalf("status/refreshes/password calls = %d/%d/%d", response.StatusCode, refreshes, passwordCalls)
	}
	if len(response.Cookies()) != 0 {
		t.Fatal("wrong current password cleared the browser session")
	}
	if status := request(t, bff, http.MethodGet, "/bff/session", "", cookie, "").StatusCode; status != http.StatusOK {
		t.Fatalf("session status after password rejection = %d", status)
	}
}

func TestSelfPasswordDomain401AfterRefreshDoesNotDiscardSession(t *testing.T) {
	var refreshes, passwordCalls int
	_, bff := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			loginAPI(w, r)
		case "/api/v1/auth/refresh":
			refreshes++
			fmt.Fprint(w, `{"data":{"access_token":"fresh-access","refresh_token":"fresh-refresh"}}`)
		case "/api/v1/profile/password":
			passwordCalls++
			if r.Header.Get("Authorization") == "Bearer old-access" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"success":false,"message":"current password is incorrect"}`)
		default:
			t.Errorf("unexpected upstream route %s", r.URL.Path)
		}
	})
	cookie, csrf := signedIn(t, bff)
	response := request(t, bff, http.MethodPatch, "/bff/api/profile/password", `{"current_password":"wrong","new_password":"replacement"}`, cookie, csrf)
	if response.StatusCode != http.StatusUnprocessableEntity || refreshes != 1 || passwordCalls != 2 {
		t.Fatalf("status/refreshes/password calls = %d/%d/%d", response.StatusCode, refreshes, passwordCalls)
	}
	if len(response.Cookies()) != 0 {
		t.Fatal("password validation failure after access-token refresh cleared the browser session")
	}
}

func TestSelfPasswordSuccessClearsSession(t *testing.T) {
	s, bff := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			loginAPI(w, r)
			return
		}
		if r.URL.Path != "/api/v1/profile/password" {
			t.Errorf("unexpected upstream route %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"message":"password updated successfully","data":null}`)
	})
	cookie, csrf := signedIn(t, bff)
	response := request(t, bff, http.MethodPatch, "/bff/api/profile/password", `{"current_password":"old","new_password":"replacement"}`, cookie, csrf)
	if response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 || response.Cookies()[0].MaxAge >= 0 {
		t.Fatalf("password success did not clear cookie: status=%d cookies=%v", response.StatusCode, response.Cookies())
	}
	s.mu.Lock()
	_, retained := s.sessions[cookie.Value]
	s.mu.Unlock()
	if retained {
		t.Fatal("password change retained the old BFF session")
	}
	check := request(t, bff, http.MethodGet, "/bff/session", "", cookie, "")
	var sessionState struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.NewDecoder(check.Body).Decode(&sessionState); err != nil || sessionState.Authenticated {
		t.Fatalf("session after password change was authenticated: %v", err)
	}
}

func TestWorkspaceMutationsRetainCSRFAndUpstreamAuthority(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/apps/11111111-1111-4111-8111-111111111111/templates", `{"name":"fixture"}`},
		{"PUT", "/apps/11111111-1111-4111-8111-111111111111/segments/7/members/42", ""},
		{"DELETE", "/apps/11111111-1111-4111-8111-111111111111/segments/7", ""},
		{"PATCH", "/profile", `{"name":"fixture"}`},
		{"PATCH", "/profile/password", `{"current_password":"old","new_password":"replacement"}`},
		{"PATCH", "/staff/42/password", `{"current_password":"actor","password":"replacement"}`},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			calls := 0
			_, bff := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/auth/login" {
					loginAPI(w, r)
					return
				}
				calls++
				if r.Header.Get("Authorization") != "Bearer old-access" || r.Header.Get("X-API-Key") != "private-key" || r.Method != tc.method || r.URL.Path != "/api/v1"+tc.path {
					t.Error("incorrect upstream identity or destination")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"permission denied"}`))
			})
			cookie, csrf := signedIn(t, bff)
			if result := request(t, bff, tc.method, "/bff/api"+tc.path, tc.body, cookie, ""); result.StatusCode != 403 || calls != 0 {
				t.Fatal("mutation bypassed CSRF")
			}
			if result := request(t, bff, tc.method, "/bff/api"+tc.path, tc.body, cookie, csrf); result.StatusCode != 403 || calls != 1 {
				t.Fatal("upstream authorization denial was not preserved")
			}
		})
	}
}
