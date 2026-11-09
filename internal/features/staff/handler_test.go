package staff

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaffJSONHandlersRequireJSONMediaType(t *testing.T) {
	for _, endpoint := range []struct {
		name string
		path string
		hit  func(*StaffHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "create", path: "/api/v1/staff", hit: (*StaffHandler).Create},
		{name: "update", path: "/api/v1/staff/1", hit: (*StaffHandler).Update},
		{name: "update status", path: "/api/v1/staff/1/status", hit: (*StaffHandler).UpdateStatus},
		{name: "update password", path: "/api/v1/staff/1/password", hit: (*StaffHandler).UpdatePassword},
	} {
		for _, mediaType := range []struct {
			name  string
			value string
		}{
			{name: "missing"},
			{name: "wrong", value: "text/plain"},
		} {
			t.Run(endpoint.name+"/"+mediaType.name, func(t *testing.T) {
				handler := NewStaffHandler(nil)
				req := httptest.NewRequest(http.MethodPatch, endpoint.path, strings.NewReader(`{}`))
				if endpoint.name == "create" {
					req.Method = http.MethodPost
				}
				if mediaType.value != "" {
					req.Header.Set("Content-Type", mediaType.value)
				}
				if endpoint.name != "create" {
					req.SetPathValue("id", "1")
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
