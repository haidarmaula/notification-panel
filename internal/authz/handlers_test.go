package authz_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"hello/internal/authz"
	"hello/internal/features/notifications"
	"hello/internal/features/segments"
	"hello/internal/features/staff"
)

func TestEveryHandlerMapsAuthorizationErrors(t *testing.T) {
	for _, tc := range []struct {
		name, role string
		err        error
		status     int
		message    string
	}{
		{name: "unauthenticated", err: pgx.ErrNoRows, status: 401, message: "authentication required"},
		{name: "forbidden", role: "CUSTOM", status: 403, message: "insufficient permissions"},
		{name: "internal", err: errors.New("private SQL password detail"), status: 500, message: "internal server error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker := authz.New(authority{role: tc.role, err: tc.err})
			n := notifications.NewNotificationHandler(notifications.NewNotificationService(nil, nil, nil, nil, nil, nil, nil, nil, nil, checker))
			s := segments.NewSegmentHandler(segments.NewSegmentService(nil, nil, nil, nil, checker))
			m := segments.NewMembersHandler(segments.NewMembersService(nil, nil, nil, checker))
			staffHandler := staff.NewStaffHandler(staff.NewStaffService(nil, nil, checker))
			handlers := []struct {
				name    string
				handler http.HandlerFunc
				body    string
			}{
				{"staff.list", staffHandler.List, ""}, {"staff.get", staffHandler.GetByID, ""},
				{"staff.create", staffHandler.Create, `{"role":"ADMIN","name":"Fixture","email":"fixture@example.invalid","password":"fixture-password"}`},
				{"staff.update", staffHandler.Update, `{"name":"Fixture"}`}, {"staff.status", staffHandler.UpdateStatus, `{"is_active":true}`},
				{"staff.password", staffHandler.UpdatePassword, `{"current_password":"fixture-password","password":"new-password"}`},
				{"staff.delete", staffHandler.Delete, ""},
				{"notification.list", n.List, ""}, {"notification.get", n.GetByID, ""},
				{"notification.create", n.Create, `{"title":"Fixture","body":"Fixture"}`},
				{"notification.update", n.Update, `{"title":"Fixture"}`}, {"notification.delete", n.Delete, ""}, {"notification.send", n.Send, ""},
				{"segment.list", s.List, ""}, {"segment.get", s.GetByID, ""}, {"segment.create", s.Create, `{"name":"Fixture"}`},
				{"segment.update", s.Update, `{"name":"Fixture"}`}, {"segment.delete", s.Delete, ""},
				{"members.list", m.ListMembers, ""}, {"members.add", m.AddMember, `{"user_id":7}`}, {"members.remove", m.RemoveMember, ""},
			}
			for _, h := range handlers {
				t.Run(h.name, func(t *testing.T) {
					r := httptest.NewRequest("POST", "/", strings.NewReader(h.body)).WithContext(serviceIdentity())
					r.SetPathValue("id", "42")
					r.SetPathValue("userId", "7")
					w := httptest.NewRecorder()
					h.handler(w, r)
					require.Equal(t, tc.status, w.Code)
					require.Contains(t, w.Body.String(), tc.message)
					require.NotContains(t, w.Body.String(), "private")
				})
			}
		})
	}
}
