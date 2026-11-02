package authz_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/features/notifications"
	"hello/internal/features/segments"
	"hello/internal/middleware"
	"hello/internal/token"
)

type authority struct {
	role string
	err  error
}

func (a authority) GetStaffAuthority(ctx context.Context, id int64) (sqlc.GetStaffAuthorityRow, error) {
	return sqlc.GetStaffAuthorityRow{ID: id, IsActive: true, RoleName: pgtype.Text{String: a.role, Valid: true}}, a.err
}
func serviceIdentity() context.Context {
	return context.WithValue(context.Background(), middleware.UserContextKey, &token.AccessClaims{StaffID: 7, RoleName: "SUPER_ADMIN"})
}

func TestDirectServiceDenialBeforeDomainIO(t *testing.T) {
	ctx := serviceIdentity()
	for _, tc := range []struct {
		role string
		err  error
		want error
	}{
		{role: "VIEWER", want: authz.ErrForbidden}, {role: "CUSTOM", want: authz.ErrForbidden}, {err: errors.New("database unavailable")},
	} {
		t.Run(tc.role, func(t *testing.T) {
			checker := authz.New(authority{tc.role, tc.err})
			want := tc.want
			if tc.err != nil {
				want = tc.err
			}
			n := notifications.NewNotificationService(nil, nil, nil, nil, nil, nil, nil, nil, nil, checker)
			s := segments.NewSegmentService(nil, nil, nil, nil, checker)
			m := segments.NewMembersService(nil, nil, nil, checker)
			// Nil domain collaborators make any accidental domain I/O fail the test.
			_, err := n.Create(ctx, notifications.CreateParams{})
			require.ErrorIs(t, err, want)
			require.ErrorIs(t, n.Update(ctx, 42, notifications.UpdateParams{}), want)
			require.ErrorIs(t, n.Delete(ctx, 42), want)
			require.ErrorIs(t, n.Send(ctx, 42), want)
			_, err = s.Create(ctx, segments.CreateParams{})
			require.ErrorIs(t, err, want)
			_, err = s.Update(ctx, segments.UpdateParams{})
			require.ErrorIs(t, err, want)
			require.ErrorIs(t, s.Delete(ctx, 42), want)
			require.ErrorIs(t, m.AddMember(ctx, 42, 7), want)
			require.ErrorIs(t, m.RemoveMember(ctx, 42, 7), want)
			if tc.role != "VIEWER" {
				_, _, err = n.List(ctx, 1, 10, "", "", "")
				require.ErrorIs(t, err, want)
				_, err = n.GetByID(ctx, 42)
				require.ErrorIs(t, err, want)
				_, _, err = s.List(ctx, 1, 10, "")
				require.ErrorIs(t, err, want)
				_, err = s.GetByID(ctx, 42)
				require.ErrorIs(t, err, want)
				_, _, err = m.ListMembers(ctx, 42, 1, 10)
				require.ErrorIs(t, err, want)
			}
		})
	}
	t.Run("operator cannot schedule via service", func(t *testing.T) {
		checker := authz.New(authority{role: "OPERATOR"})
		n := notifications.NewNotificationService(nil, nil, nil, nil, nil, nil, nil, nil, nil, checker)
		later := time.Now().Add(time.Hour)
		_, err := n.Create(ctx, notifications.CreateParams{ScheduledAt: &later})
		require.ErrorIs(t, err, authz.ErrForbidden)
		require.ErrorIs(t, n.Update(ctx, 42, notifications.UpdateParams{ScheduledAt: &later}), authz.ErrForbidden)
		require.ErrorIs(t, n.Send(ctx, 42), authz.ErrForbidden)
	})
	require.Panics(t, func() { notifications.NewNotificationService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil) })
	require.Panics(t, func() { segments.NewSegmentService(nil, nil, nil, nil, nil) })
	require.Panics(t, func() { segments.NewMembersService(nil, nil, nil, nil) })
}
