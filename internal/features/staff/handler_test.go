package staff

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetPaginationParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		query     string
		page      int32
		limit     int32
		wantError error
	}{
		{name: "defaults", page: 1, limit: 10},
		{name: "valid values", query: "?page=3&limit=25", page: 3, limit: 25},
		{name: "maximum safe offset", query: "?page=21474837&limit=100", page: 21474837, limit: 100},
		{name: "malformed page", query: "?page=x", wantError: ErrInvalidPagination},
		{name: "zero page", query: "?page=0", wantError: ErrInvalidPagination},
		{name: "duplicate page", query: "?page=1&page=2", wantError: ErrInvalidPagination},
		{name: "empty limit", query: "?limit=", wantError: ErrInvalidPagination},
		{name: "limit too large", query: "?limit=101", wantError: ErrInvalidPagination},
		{name: "offset overflows", query: "?page=21474838&limit=100", wantError: ErrInvalidPagination},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/staff"+tc.query, nil)
			page, limit, err := getPaginationParams(req)
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("error = %v, want %v", err, tc.wantError)
			}
			if err == nil && (page != tc.page || limit != tc.limit) {
				t.Fatalf("page/limit = %d/%d, want %d/%d", page, limit, tc.page, tc.limit)
			}
		})
	}
}

func TestStaffListReturnsBadRequestForInvalidPagination(t *testing.T) {
	t.Parallel()

	for _, query := range []string{"?limit=101", "?page=21474838&limit=100"} {
		query := query
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			handler := NewStaffHandler(nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/staff"+query, nil)
			w := httptest.NewRecorder()
			handler.List(w, req)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), ErrInvalidPagination.Error()) {
				t.Fatalf("response = %d %s, want 400 invalid pagination", w.Code, w.Body.String())
			}
		})
	}
}

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
