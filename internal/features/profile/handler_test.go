package profile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hello/internal/middleware"
	"hello/internal/token"
)

func TestProfileJSONHandlersRequireJSONMediaType(t *testing.T) {
	for _, endpoint := range []struct {
		name string
		path string
		hit  func(*ProfileHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "update profile", path: "/api/v1/profile", hit: (*ProfileHandler).UpdateProfile},
		{name: "update password", path: "/api/v1/profile/password", hit: (*ProfileHandler).UpdatePassword},
	} {
		for _, mediaType := range []struct {
			name  string
			value string
		}{
			{name: "missing"},
			{name: "wrong", value: "text/plain"},
		} {
			t.Run(endpoint.name+"/"+mediaType.name, func(t *testing.T) {
				handler := NewProfileHandler(nil)
				req := httptest.NewRequest(http.MethodPatch, endpoint.path, strings.NewReader(`{}`))
				req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, &token.AccessClaims{StaffID: 1}))
				if mediaType.value != "" {
					req.Header.Set("Content-Type", mediaType.value)
				}
				w := httptest.NewRecorder()

				endpoint.hit(handler, w, req)

				if w.Code != http.StatusUnsupportedMediaType {
					t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusUnsupportedMediaType, w.Body.String())
				}
				if strings.Contains(w.Body.String(), "internal server error") {
					t.Fatalf("request reached service: %s", w.Body.String())
				}
			})
		}
	}
}
