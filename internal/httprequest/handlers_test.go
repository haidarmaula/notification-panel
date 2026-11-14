package httprequest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"hello/internal/features/applications"
	"hello/internal/features/auth"
	"hello/internal/features/profile"
	"hello/internal/features/staff"
	"hello/internal/httprequest"
	"hello/internal/middleware"
	"hello/internal/token"
)

func TestAllJSONHandlersRejectBeforeService(t *testing.T) {
	// Nil services make accidental business calls fail the test immediately.
	a := auth.NewAuthHandler(nil)
	apps := &applications.Handler{}
	p := profile.NewProfileHandler(nil)
	staffHandler := staff.NewStaffHandler(nil)
	for _, tc := range []struct {
		name  string
		h     http.HandlerFunc
		limit int64
	}{
		{"auth.login", a.Login, httprequest.DefaultJSONLimit}, {"auth.refresh", a.RefreshToken, httprequest.DefaultJSONLimit},
		{"auth.logout", a.Logout, httprequest.DefaultJSONLimit},
		{"campaign.create", apps.CreateCampaignDraft, httprequest.DefaultJSONLimit},
		{"profile.update", p.UpdateProfile, httprequest.DefaultJSONLimit}, {"profile.password", p.UpdatePassword, httprequest.DefaultJSONLimit},
		{"staff.create", staffHandler.Create, httprequest.DefaultJSONLimit}, {"staff.update", staffHandler.Update, httprequest.DefaultJSONLimit},
		{"staff.status", staffHandler.UpdateStatus, httprequest.DefaultJSONLimit}, {"staff.password", staffHandler.UpdatePassword, httprequest.DefaultJSONLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := `{"title":"Fixture","body":"Fixture","role":"ADMIN","name":"Fixture","email":"fixture@example.invalid","password":"fixture-password","current_password":"fixture-password","jwt":"fixture-jwt","push_token":"fixture-token","platform":"android","user_id":1}`
			if tc.name == "campaign.create" {
				prefix = `{"title":"Fixture","body":"Fixture"}`
			}
			for _, variant := range []struct {
				name, body string
				want       int
			}{
				{"oversized value", `{"padding":"` + strings.Repeat("x", int(tc.limit)) + `"}`, 413},
				{"valid prefix oversized whitespace", prefix + strings.Repeat(" ", int(tc.limit)), 413},
				{"multiple values", prefix + `{}`, 400},
			} {
				t.Run(variant.name, func(t *testing.T) {
					ctx := context.WithValue(context.Background(), middleware.UserContextKey, &token.AccessClaims{StaffID: 1, RoleName: "SUPER_ADMIN"})
					r := httptest.NewRequest("POST", "/", strings.NewReader(variant.body)).WithContext(ctx)
					r.Header.Set("Content-Type", "application/json")
					r.ContentLength = -1
					r.SetPathValue("id", "1")
					r.SetPathValue("app_id", "123e4567-e89b-12d3-a456-426614174000")
					w := httptest.NewRecorder()
					tc.h(w, r)
					require.Equal(t, variant.want, w.Code)
					require.NotContains(t, w.Body.String(), "fixture-password")
				})
			}
		})
	}
}
