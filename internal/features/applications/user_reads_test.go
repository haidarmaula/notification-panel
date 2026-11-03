package applications

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

type scopedReadFixture struct {
	sqlc.Querier
	id     pgtype.UUID
	status string
	grant  bool
}

func (q scopedReadFixture) GetApplicationAuthority(context.Context, sqlc.GetApplicationAuthorityParams) (sqlc.GetApplicationAuthorityRow, error) {
	return sqlc.GetApplicationAuthorityRow{
		StaffID: 7, StaffIsActive: true, RoleName: pgtype.Text{String: "SUPER_ADMIN", Valid: true},
		ApplicationID: pgtype.Int8{Int64: 42, Valid: true}, ApplicationPublicID: q.id,
		ApplicationStatus: pgtype.Text{String: q.status, Valid: true},
		GrantIsActive:     pgtype.Bool{Bool: q.grant, Valid: true},
	}, nil
}
func (q scopedReadFixture) GetScopedUser(_ context.Context, arg sqlc.GetScopedUserParams) (sqlc.GetScopedUserRow, error) {
	if arg.ApplicationID == 42 && arg.UserID == 9 {
		return sqlc.GetScopedUserRow{ID: 9}, nil
	}
	return sqlc.GetScopedUserRow{}, pgx.ErrNoRows
}
func (q scopedReadFixture) ListScopedUserDevices(_ context.Context, arg sqlc.ListScopedUserDevicesParams) ([]sqlc.ListScopedUserDevicesRow, error) {
	if arg != (sqlc.ListScopedUserDevicesParams{ApplicationID: 42, UserID: 9, PageSize: 2}) {
		panic("unexpected device scope or pagination")
	}
	return []sqlc.ListScopedUserDevicesRow{{ID: 9007199254740993, Platform: "ANDROID"}, {ID: 9007199254740994}}, nil
}

func TestScopedDeviceHTTP(t *testing.T) {
	const app = "7a740442-d2b9-4d60-b578-8bf05022d68e"
	var id pgtype.UUID
	require.NoError(t, id.Scan(app))
	for _, tc := range []struct {
		name, status, user, query string
		grant                     bool
		code                      int
	}{
		{"active", "ACTIVE", "9", "?limit=1", true, 200},
		{"no super admin bypass", "ACTIVE", "9", "?limit=1", false, 404},
		{"provisioning", "PROVISIONING", "9", "?limit=1", true, 409},
		{"missing parent", "ACTIVE", "10", "?limit=1", true, 404},
		{"duplicate parameter", "ACTIVE", "9", "?limit=1&limit=2", true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := scopedReadFixture{id: id, status: tc.status, grant: tc.grant}
			h := NewHandler(&Service{q: fixture})
			r := httptest.NewRequest(http.MethodGet, "/devices"+tc.query, nil)
			r.SetPathValue("app_id", app)
			r.SetPathValue("user_id", tc.user)
			r = r.WithContext(context.WithValue(r.Context(), middleware.UserContextKey, &token.AccessClaims{StaffID: 7}))
			w := httptest.NewRecorder()
			h.ListUserDevices(w, r)
			require.Equal(t, tc.code, w.Code, w.Body.String())
			if tc.code == 200 {
				require.JSONEq(t, `{"success":true,"message":"success","data":{"items":[{"id":"9007199254740993","platform":"ANDROID","is_active":false,"last_seen_at":null,"created_at":null,"updated_at":null}],"next_after_id":"9007199254740993"}}`, w.Body.String())
			}
		})
	}
}
