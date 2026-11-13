package dashboard

import (
	"net/http"
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
	} {
		t.Run(tc.method+tc.suffix, func(t *testing.T) {
			if got := allowed(tc.method, base+tc.suffix); got != tc.want {
				t.Fatalf("allowed = %v, want %v", got, tc.want)
			}
		})
	}
	if !allowed("PATCH", "/profile") || allowed("PATCH", "/profile/password") || allowed("PATCH", "/staff/1") {
		t.Fatal("profile access must not expand to credentials or staff administration")
	}
}

func TestWorkspaceMutationsRetainCSRFAndUpstreamAuthority(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/apps/11111111-1111-4111-8111-111111111111/templates", `{"name":"fixture"}`},
		{"PUT", "/apps/11111111-1111-4111-8111-111111111111/segments/7/members/42", ""},
		{"DELETE", "/apps/11111111-1111-4111-8111-111111111111/segments/7", ""},
		{"PATCH", "/profile", `{"name":"fixture"}`},
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
