package staff

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/features/staff/mocks"
)

type authorityFunc func(context.Context, int64) (sqlc.GetStaffAuthorityRow, error)

func (f authorityFunc) GetStaffAuthority(ctx context.Context, id int64) (sqlc.GetStaffAuthorityRow, error) {
	return f(ctx, id)
}

func TestAllStaffServicesFailClosed(t *testing.T) {
	dbError := errors.New("private authority failure")
	for _, tc := range []struct {
		name, role   string
		active       bool
		lookup, want error
		missing      bool
	}{
		{name: "missing identity", missing: true, want: authz.ErrUnauthenticated},
		{name: "viewer", role: "VIEWER", active: true, want: authz.ErrForbidden},
		{name: "operator", role: "OPERATOR", active: true, want: authz.ErrForbidden},
		{name: "admin", role: "ADMIN", active: true, want: authz.ErrForbidden},
		{name: "unknown", role: "CUSTOM", active: true, want: authz.ErrForbidden},
		{name: "inactive", role: "SUPER_ADMIN", want: authz.ErrUnauthenticated},
		{name: "deleted", lookup: pgx.ErrNoRows, want: authz.ErrUnauthenticated},
		{name: "database failure", lookup: dbError, want: dbError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker := authz.New(authorityFunc(func(_ context.Context, id int64) (sqlc.GetStaffAuthorityRow, error) {
				return sqlc.GetStaffAuthorityRow{ID: id, IsActive: tc.active, RoleName: pgtype.Text{String: tc.role, Valid: true}}, tc.lookup
			}))
			// Nil business collaborators prove every operation denies before business I/O.
			service := newOperations(nil, nil, nil, checker)
			ctx := ctxWithStaffID(99)
			if tc.missing {
				ctx = context.Background()
			}
			operations := map[string]func() error{
				"create":   func() error { _, err := service.Create(ctx, CreateStaffParams{}); return err },
				"get":      func() error { _, err := service.GetByID(ctx, 1); return err },
				"list":     func() error { _, _, err := service.List(ctx, 1, 10, ""); return err },
				"update":   func() error { _, err := service.Update(ctx, UpdateStaffParams{ID: 1}); return err },
				"status":   func() error { _, err := service.UpdateStatus(ctx, 1, false); return err },
				"password": func() error { return service.UpdatePassword(ctx, 1, "current-password", "new-password") },
				"delete":   func() error { return service.Delete(ctx, 1) },
			}
			for name, op := range operations {
				t.Run(name, func(t *testing.T) { require.ErrorIs(t, op(), tc.want) })
			}
		})
	}
	require.Panics(t, func() { newOperations(nil, nil, nil, nil) })
}

func TestPasswordActorLookupFailsClosed(t *testing.T) {
	dbError := errors.New("private actor lookup failure")
	inactive := dummyStaffUser(99)
	inactive.IsActive = false
	mismatched := dummyStaffUser(100)
	for _, tc := range []struct {
		name         string
		row          sqlc.StaffUser
		lookup, want error
	}{
		{name: "deleted after authority check", lookup: pgx.ErrNoRows, want: authz.ErrUnauthenticated},
		{name: "inactive after authority check", row: inactive, want: authz.ErrUnauthenticated},
		{name: "database failure", lookup: dbError, want: dbError},
		{name: "identity mismatch", row: mismatched},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := mocks.NewMockStaffUserRepository(t)
			repo.EXPECT().FindByID(mock.Anything, int64(99)).Return(tc.row, tc.lookup).Once()
			s := newOperations(repo, nil, nil, testAuthority("SUPER_ADMIN"))
			err := s.UpdatePassword(ctxWithStaffID(99), 1, "fixture-password", "new-password")
			require.Error(t, err)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			}
		})
	}
}
