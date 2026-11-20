package dashboard

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const onboardingTestPath = "/admin/apps/11111111-1111-4111-8111-111111111111/onboarding"
const onboardingTestOperation = "22222222-2222-4222-8222-222222222222"

func TestOnboardingAllowlist(t *testing.T) {
	for _, tc := range []struct {
		method, suffix string
		want           bool
	}{
		{"GET", "", true}, {"GET", "/enrollment-profiles", true}, {"GET", "/approvals", true},
		{"PUT", "/enrollment", true}, {"PUT", "/fcm", true}, {"POST", "/fcm/verify", true},
		{"POST", "/dispatch/hold", true}, {"GET", "/operations/" + onboardingTestOperation, true},
		{"GET", "/", false}, {"GET", "/fcm", false}, {"POST", "/fcm", false},
		{"PUT", "/fcm/verify", false}, {"GET", "/operations/not-a-uuid", false},
		{"GET", "/operations/" + onboardingTestOperation + "/extra", false},
		{"POST", "/activate", true}, {"POST", "/test-cohort", false}, {"PUT", "/test-cohort", true},
		{"GET", "/test-installations", true}, {"GET", "/operations/" + onboardingTestOperation + "/targets", true},
		{"POST", "/dispatch/release", true}, {"GET", "/dispatch/release", false}, {"POST", "/fcm/promote", false},
		{"DELETE", "", false}, {"PATCH", "/enrollment", false},
	} {
		if got := allowed(tc.method, onboardingTestPath+tc.suffix); got != tc.want {
			t.Errorf("%s %s = %v, want %v", tc.method, tc.suffix, got, tc.want)
		}
	}
	for _, location := range []string{
		"https://external.test/operations/" + onboardingTestOperation,
		"/api/v1" + strings.Replace(onboardingTestPath, "11111111", "33333333", 1) + "/operations/" + onboardingTestOperation,
		"/api/v1" + onboardingTestPath + "/operations/" + onboardingTestOperation + "?secret=value",
	} {
		if got := onboardingOperationLocation(onboardingTestPath+"/fcm/verify", location); got != "" {
			t.Errorf("unsafe operation location accepted: %s", got)
		}
	}
}

func TestOnboardingForwardingPreservesConcurrencyAndPolling(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusAccepted, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			_, bff := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/auth/login" {
					loginAPI(w, r)
					return
				}
				if r.Header.Get("If-Match") != `"setup:1"` || r.Header.Get("Idempotency-Key") != onboardingTestOperation || r.Header.Get("If-None-Match") != "" {
					t.Error("onboarding concurrency headers were changed")
				}
				w.Header().Set("ETag", `"setup:2"`)
				w.Header().Set("Location", "/api/v1"+onboardingTestPath+"/operations/"+onboardingTestOperation)
				w.Header().Set("Retry-After", "5")
				w.Header().Set("Idempotency-Replayed", "true")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"data":{}}`)
			})
			cookie, csrf := signedIn(t, bff)
			r, err := http.NewRequest(http.MethodPost, bff.URL+"/bff/api"+onboardingTestPath+"/fcm/verify", strings.NewReader(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Origin", "http://127.0.0.1:5173")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", csrf)
			r.Header.Set("If-Match", `"setup:1"`)
			r.Header.Set("If-None-Match", "*")
			r.Header.Set("Idempotency-Key", onboardingTestOperation)
			r.AddCookie(cookie)
			response, err := bff.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != status || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected response status/cache policy: %d", response.StatusCode)
			}
			if status != http.StatusServiceUnavailable && response.Header.Get("ETag") != `"setup:2"` {
				t.Error("onboarding ETag was dropped")
			}
			if status != http.StatusServiceUnavailable && response.Header.Get("Idempotency-Replayed") != "true" {
				t.Error("idempotency replay acknowledgement was dropped")
			}
			if status == http.StatusAccepted && response.Header.Get("Location") != "/bff/api"+onboardingTestPath+"/operations/"+onboardingTestOperation {
				t.Error("operation location was not rewritten")
			}
			if (status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable) && response.Header.Get("Retry-After") != "5" {
				t.Error("bounded retry delay was dropped")
			}
		})
	}
}

func TestOnboardingRejectsQueriesAndDuplicateConcurrencyHeaders(t *testing.T) {
	var calls int
	_, bff := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			loginAPI(w, r)
			return
		}
		calls++
		fmt.Fprint(w, `{"data":{}}`)
	})
	cookie, csrf := signedIn(t, bff)
	for _, variant := range []string{"?unknown=value", "?", "If-Match", "Idempotency-Key"} {
		path := bff.URL + "/bff/api" + onboardingTestPath + "/fcm/verify"
		if strings.HasPrefix(variant, "?") {
			path += variant
		}
		r, err := http.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Origin", "http://127.0.0.1:5173")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		r.AddCookie(cookie)
		if !strings.HasPrefix(variant, "?") {
			r.Header.Add(variant, "first")
			r.Header.Add(variant, "second")
		}
		response, err := bff.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest || calls != 0 {
			t.Fatalf("%s reached upstream or was accepted", variant)
		}
	}
}

func TestOnboardingCredentialBodyLimitIsScoped(t *testing.T) {
	var received int
	_, bff := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			loginAPI(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		received = len(body)
		fmt.Fprint(w, `{"data":{}}`)
	})
	cookie, csrf := signedIn(t, bff)
	for _, tc := range []struct {
		path   string
		size   int
		status int
	}{
		{onboardingTestPath + "/fcm", 70 * 1024, http.StatusOK},
		{onboardingTestPath + "/fcm", 96*1024 + 1, http.StatusRequestEntityTooLarge},
		{onboardingTestPath + "/enrollment", 70 * 1024, http.StatusRequestEntityTooLarge},
	} {
		received = 0
		response := request(t, bff, http.MethodPut, "/bff/api"+tc.path, strings.Repeat(" ", tc.size), cookie, csrf)
		response.Body.Close()
		if response.StatusCode != tc.status || (tc.status == http.StatusOK && received != tc.size) || (tc.status != http.StatusOK && received != 0) {
			t.Fatalf("%s size %d: status=%d, upstream bytes=%d", tc.path, tc.size, response.StatusCode, received)
		}
	}
}
