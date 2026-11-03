package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
)

type appAuthorityFunc func(context.Context, sqlc.GetApplicationAuthorityParams) (sqlc.GetApplicationAuthorityRow, error)

func (f appAuthorityFunc) GetApplicationAuthority(c context.Context, p sqlc.GetApplicationAuthorityParams) (sqlc.GetApplicationAuthorityRow, error) {
	return f(c, p)
}
func TestApplicationScopePolicy(t *testing.T) {
	id := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	base := sqlc.GetApplicationAuthorityRow{StaffID: 7, StaffIsActive: true, RoleName: pgtype.Text{String: "SUPER_ADMIN", Valid: true}, ApplicationID: pgtype.Int8{Int64: 1, Valid: true}, ApplicationPublicID: id, ApplicationStatus: pgtype.Text{String: "PROVISIONING", Valid: true}, GrantIsActive: pgtype.Bool{Bool: true, Valid: true}}
	dbError := errors.New("private authority failure")
	for _, tc := range []struct {
		name         string
		change       func(*sqlc.GetApplicationAuthorityRow)
		permission   Permission
		lookup, want error
		missing      bool
	}{
		{name: "metadata", permission: ApplicationsRead},
		{name: "viewer metadata", permission: ApplicationsRead, change: func(r *sqlc.GetApplicationAuthorityRow) { r.RoleName.String = "VIEWER" }},
		{name: "missing identity", missing: true, want: ErrUnauthenticated},
		{name: "deleted staff", lookup: pgx.ErrNoRows, want: ErrUnauthenticated},
		{name: "inactive", change: func(r *sqlc.GetApplicationAuthorityRow) { r.StaffIsActive = false }, want: ErrUnauthenticated},
		{name: "unknown role", change: func(r *sqlc.GetApplicationAuthorityRow) { r.RoleName.String = "CUSTOM" }, want: ErrForbidden},
		{name: "no super wildcard", change: func(r *sqlc.GetApplicationAuthorityRow) { r.GrantIsActive.Valid = false }, want: ErrApplicationNotFound},
		{name: "revoked grant", change: func(r *sqlc.GetApplicationAuthorityRow) { r.GrantIsActive.Bool = false }, want: ErrApplicationNotFound},
		{name: "missing app", change: func(r *sqlc.GetApplicationAuthorityRow) { r.ApplicationID.Valid = false }, want: ErrApplicationNotFound},
		{name: "viewer mutation", permission: NotificationsWrite, change: func(r *sqlc.GetApplicationAuthorityRow) { r.RoleName.String = "VIEWER" }, want: ErrForbidden},
		{name: "provisioning cannot send", permission: NotificationsSend, want: ErrApplicationNotActive},
		{name: "unknown operation", permission: "unknown", want: ErrForbidden},
		{name: "global staff operation is not app authority", permission: StaffManage, change: func(r *sqlc.GetApplicationAuthorityRow) { r.ApplicationStatus.String = "ACTIVE" }, want: ErrForbidden},
		{name: "global project administration is not app authority", permission: ApplicationsManage, change: func(r *sqlc.GetApplicationAuthorityRow) { r.ApplicationStatus.String = "ACTIVE" }, want: ErrForbidden},
		{name: "database failure", lookup: dbError, want: dbError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := base
			if tc.change != nil {
				tc.change(&row)
			}
			a := NewApplicationAuthorizer(appAuthorityFunc(func(context.Context, sqlc.GetApplicationAuthorityParams) (sqlc.GetApplicationAuthorityRow, error) {
				return row, tc.lookup
			}))
			ctx := identity(7, "VIEWER")
			if tc.missing {
				ctx = context.Background()
			}
			p := tc.permission
			if p == "" {
				p = ApplicationsRead
			}
			got, err := a.RequireApplication(ctx, id, p)
			require.ErrorIs(t, err, tc.want)
			if tc.want != nil {
				require.Empty(t, got)
			} else {
				require.Equal(t, int64(1), got.ApplicationID)
				require.Equal(t, row.RoleName.String, got.Staff.Role)
			}
		})
	}
	calls := 0
	a := NewApplicationAuthorizer(appAuthorityFunc(func(context.Context, sqlc.GetApplicationAuthorityParams) (sqlc.GetApplicationAuthorityRow, error) {
		calls++
		return base, nil
	}))
	_, err := a.RequireApplication(identity(7, "SUPER_ADMIN"), id, ApplicationsRead)
	require.NoError(t, err)
	base.GrantIsActive.Bool = false
	_, err = a.RequireApplication(identity(7, "SUPER_ADMIN"), id, ApplicationsRead)
	require.ErrorIs(t, err, ErrApplicationNotFound)
	require.Equal(t, 2, calls)
	ctx, cancel := context.WithCancel(identity(7, "SUPER_ADMIN"))
	cancel()
	_, err = a.RequireApplication(ctx, id, ApplicationsRead)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 2, calls)
	require.Panics(t, func() { NewApplicationAuthorizer(nil) })
	var zero ApplicationAuthorizer
	_, err = zero.RequireApplication(identity(7, "SUPER_ADMIN"), id, ApplicationsRead)
	require.Error(t, err)
	for _, change := range []func(*sqlc.GetApplicationAuthorityRow){
		func(r *sqlc.GetApplicationAuthorityRow) { r.StaffID = 8 },
		func(r *sqlc.GetApplicationAuthorityRow) { r.RoleName.Valid = false },
		func(r *sqlc.GetApplicationAuthorityRow) { r.ApplicationPublicID.Bytes[0] = 2 },
		func(r *sqlc.GetApplicationAuthorityRow) { r.ApplicationStatus.String = "UNKNOWN" },
	} {
		row := base
		row.GrantIsActive.Bool = true
		change(&row)
		a := NewApplicationAuthorizer(appAuthorityFunc(func(context.Context, sqlc.GetApplicationAuthorityParams) (sqlc.GetApplicationAuthorityRow, error) {
			return row, nil
		}))
		got, err := a.RequireApplication(identity(7, "SUPER_ADMIN"), id, ApplicationsRead)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrForbidden)
		require.Empty(t, got)
	}
}
