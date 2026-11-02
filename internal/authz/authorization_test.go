package authz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

type authorityFunc func(context.Context, int64) (sqlc.GetStaffAuthorityRow, error)

func (f authorityFunc) GetStaffAuthority(ctx context.Context, id int64) (sqlc.GetStaffAuthorityRow, error) {
	return f(ctx, id)
}
func identity(id int64, role string) context.Context {
	return context.WithValue(context.Background(), middleware.UserContextKey, &token.AccessClaims{StaffID: id, RoleName: role})
}
func authority(id int64, role string) sqlc.GetStaffAuthorityRow {
	return sqlc.GetStaffAuthorityRow{ID: id, IsActive: true, RoleName: pgtype.Text{String: role, Valid: true}}
}

func TestPolicyContract(t *testing.T) {
	data, err := os.ReadFile("../../docs/contracts/staff-permissions.json")
	require.NoError(t, err)
	var contract struct {
		Roles       []string
		Permissions map[Permission][]string
	}
	require.NoError(t, json.Unmarshal(data, &contract))
	require.Equal(t, contract.Permissions, grants)
	for p, roles := range contract.Permissions {
		for _, role := range append(contract.Roles, "UNKNOWN", "admin", "") {
			expected := false
			for _, allowed := range roles {
				if allowed == role {
					expected = true
				}
			}
			require.Equal(t, expected, Allowed(role, p), "%s %s", role, p)
		}
	}
	for _, role := range contract.Roles {
		require.False(t, Allowed(role, "UNKNOWN"))
	}
}

func TestRequireDenials(t *testing.T) {
	dbError := errors.New("private database detail")
	canceled, cancel := context.WithCancel(identity(7, "SUPER_ADMIN"))
	cancel()
	deadline, cancelDeadline := context.WithDeadline(identity(7, "ADMIN"), time.Now().Add(-time.Second))
	defer cancelDeadline()
	cases := []struct {
		name           string
		ctx            context.Context
		row            sqlc.GetStaffAuthorityRow
		err, errorWant error
		permissions    []Permission
	}{
		{name: "missing", ctx: context.Background(), errorWant: ErrUnauthenticated},
		{name: "wrong claims", ctx: context.WithValue(context.Background(), middleware.UserContextKey, 7), errorWant: ErrUnauthenticated},
		{name: "typed nil", ctx: context.WithValue(context.Background(), middleware.UserContextKey, (*token.AccessClaims)(nil)), errorWant: ErrUnauthenticated},
		{name: "nonpositive", ctx: identity(0, "ADMIN"), errorWant: ErrUnauthenticated},
		{name: "deleted", ctx: identity(7, "ADMIN"), err: pgx.ErrNoRows, errorWant: ErrUnauthenticated},
		{name: "inactive", ctx: identity(7, "ADMIN"), row: sqlc.GetStaffAuthorityRow{ID: 7}, errorWant: ErrUnauthenticated},
		{name: "unknown role", ctx: identity(7, "ADMIN"), row: authority(7, "CUSTOM"), errorWant: ErrForbidden},
		{name: "database failure", ctx: identity(7, "ADMIN"), err: dbError, errorWant: dbError},
		{name: "canceled", ctx: canceled, errorWant: context.Canceled},
		{name: "deadline", ctx: deadline, errorWant: context.DeadlineExceeded},
		{name: "unknown operation", ctx: identity(7, "ADMIN"), row: authority(7, "ADMIN"), permissions: []Permission{"UNKNOWN"}, errorWant: ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New(authorityFunc(func(ctx context.Context, id int64) (sqlc.GetStaffAuthorityRow, error) { return tc.row, tc.err }))
			perms := tc.permissions
			if perms == nil {
				perms = []Permission{NotificationsSend}
			}
			principal, err := a.Require(tc.ctx, perms...)
			require.ErrorIs(t, err, tc.errorWant)
			require.Empty(t, principal)
		})
	}
	for _, row := range []sqlc.GetStaffAuthorityRow{authority(8, "ADMIN"), {ID: 7, IsActive: true}} {
		a := New(authorityFunc(func(context.Context, int64) (sqlc.GetStaffAuthorityRow, error) { return row, nil }))
		_, err := a.Require(identity(7, "ADMIN"), NotificationsRead)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrForbidden)
		require.NotErrorIs(t, err, ErrUnauthenticated)
	}
}

func TestCurrentAuthorityAndANDPermissions(t *testing.T) {
	row := authority(7, "VIEWER")
	calls := 0
	a := New(authorityFunc(func(context.Context, int64) (sqlc.GetStaffAuthorityRow, error) { calls++; return row, nil }))
	_, err := a.Require(identity(7, "SUPER_ADMIN"), NotificationsSend)
	require.ErrorIs(t, err, ErrForbidden)
	row = authority(7, "ADMIN")
	principal, err := a.Require(identity(7, "VIEWER"), NotificationsWrite, NotificationsSchedule)
	require.NoError(t, err)
	require.Equal(t, "ADMIN", principal.Role)
	row = authority(7, "OPERATOR")
	_, err = a.Require(identity(7, "ADMIN"), NotificationsWrite, NotificationsSchedule)
	require.ErrorIs(t, err, ErrForbidden)
	require.Equal(t, 3, calls)
	_, err = a.Require(identity(7, "ADMIN"))
	require.ErrorIs(t, err, ErrForbidden)
}

func TestGuardSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		err     error
		status  int
		message string
	}{
		{fmt.Errorf("wrapped: %w", ErrUnauthenticated), 401, "authentication required"},
		{fmt.Errorf("wrapped: %w", ErrForbidden), 403, "insufficient permissions"},
		{errors.New("password=private database"), 500, "internal server error"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			checker := errorChecker{tc.err}
			called := false
			handler := Guard(checker, NotificationsRead, func(http.ResponseWriter, *http.Request) { called = true })
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest("GET", "/", nil))
			require.Equal(t, tc.status, w.Code)
			require.JSONEq(t, fmt.Sprintf(`{"success":false,"message":%q}`, tc.message), w.Body.String())
			require.False(t, called)
		})
	}
	require.Panics(t, func() { New(nil) })
	require.Panics(t, func() { Guard(nil, NotificationsRead, nil) })
}

type errorChecker struct{ err error }

func (c errorChecker) Require(context.Context, ...Permission) (Principal, error) {
	return Principal{}, c.err
}

func TestCancellationDuringAuthorityLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(identity(7, "ADMIN"))
	defer cancel()
	a := New(authorityFunc(func(context.Context, int64) (sqlc.GetStaffAuthorityRow, error) {
		cancel()
		return authority(7, "ADMIN"), nil
	}))
	principal, err := a.Require(ctx, NotificationsSend)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, principal)
}
