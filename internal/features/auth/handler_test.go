package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthHandlersRequireJSONMediaType(t *testing.T) {
	for _, endpoint := range []struct {
		name string
		body string
		hit  func(*AuthHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "login", body: `{"email":"staff@example.invalid","password":"secret"}`, hit: (*AuthHandler).Login},
		{name: "refresh", body: `{"refresh_token":"secret"}`, hit: (*AuthHandler).RefreshToken},
		{name: "logout", body: `{"refresh_token":"secret"}`, hit: (*AuthHandler).Logout},
	} {
		for _, header := range []struct {
			name  string
			apply func(http.Header)
		}{
			{name: "missing", apply: func(http.Header) {}},
			{name: "wrong media type", apply: func(h http.Header) { h.Set("Content-Type", "text/plain") }},
			{name: "duplicate media type", apply: func(h http.Header) {
				h.Add("Content-Type", "application/json")
				h.Add("Content-Type", "application/json")
			}},
			{name: "compressed body", apply: func(h http.Header) {
				h.Set("Content-Type", "application/json")
				h.Set("Content-Encoding", "gzip")
			}},
		} {
			t.Run(endpoint.name+"/"+header.name, func(t *testing.T) {
				handler := NewAuthHandler(nil)
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(endpoint.body))
				header.apply(req.Header)
				w := httptest.NewRecorder()

				endpoint.hit(handler, w, req)

				if w.Code != http.StatusUnsupportedMediaType {
					t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusUnsupportedMediaType, w.Body.String())
				}
				if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "password") {
					t.Fatalf("response exposed request data: %s", w.Body.String())
				}
			})
		}
	}
}
