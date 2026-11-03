package httprequest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"hello/internal/features/auth"
	"hello/internal/features/mobile"
	"hello/internal/features/notifications"
	"hello/internal/features/profile"
	"hello/internal/features/segments"
	"hello/internal/features/staff"
	"hello/internal/features/users"
	"hello/internal/httprequest"
	"hello/internal/middleware"
	"hello/internal/token"
)

func TestAllJSONHandlersRejectBeforeService(t *testing.T) {
	// Nil services make accidental business calls fail the test immediately.
	a := auth.NewAuthHandler(nil)
	m := mobile.NewMobileHandler(nil)
	n := notifications.NewNotificationHandler(nil)
	p := profile.NewProfileHandler(nil)
	s := segments.NewSegmentHandler(nil)
	members := segments.NewMembersHandler(nil)
	staffHandler := staff.NewStaffHandler(nil)
	devices := users.NewDeviceTokenHandler(nil)
	for _, tc := range []struct {
		name  string
		h     http.HandlerFunc
		limit int64
	}{
		{"auth.login", a.Login, httprequest.DefaultJSONLimit}, {"auth.refresh", a.RefreshToken, httprequest.DefaultJSONLimit},
		{"mobile.sync", m.Sync, httprequest.DefaultJSONLimit},
		{"profile.update", p.UpdateProfile, httprequest.DefaultJSONLimit}, {"profile.password", p.UpdatePassword, httprequest.DefaultJSONLimit},
		{"notification.create", n.Create, httprequest.CampaignJSONLimit}, {"notification.update", n.Update, httprequest.CampaignJSONLimit},
		{"staff.create", staffHandler.Create, httprequest.DefaultJSONLimit}, {"staff.update", staffHandler.Update, httprequest.DefaultJSONLimit},
		{"staff.status", staffHandler.UpdateStatus, httprequest.DefaultJSONLimit}, {"staff.password", staffHandler.UpdatePassword, httprequest.DefaultJSONLimit},
		{"segment.create", s.Create, httprequest.DefaultJSONLimit}, {"segment.update", s.Update, httprequest.DefaultJSONLimit},
		{"member.add", members.AddMember, httprequest.DefaultJSONLimit},
		{"device.register", devices.Register, httprequest.DefaultJSONLimit}, {"device.update", devices.Update, httprequest.DefaultJSONLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := `{"title":"Fixture","body":"Fixture","role":"ADMIN","name":"Fixture","email":"fixture@example.invalid","password":"fixture-password","current_password":"fixture-password","jwt":"fixture-jwt","push_token":"fixture-token","platform":"android","user_id":1}`
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
					r.ContentLength = -1
					r.SetPathValue("id", "1")
					w := httptest.NewRecorder()
					tc.h(w, r)
					require.Equal(t, variant.want, w.Code)
					require.NotContains(t, w.Body.String(), "fixture-password")
				})
			}
		})
	}
}
