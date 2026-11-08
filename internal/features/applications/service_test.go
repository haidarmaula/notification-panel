package applications

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

type principalQueries struct {
	sqlc.Querier
	role   string
	err    error
	active bool
}

func (q principalQueries) GetStaffAuthority(_ context.Context, id int64) (sqlc.GetStaffAuthorityRow, error) {
	return sqlc.GetStaffAuthorityRow{ID: id, IsActive: q.active, RoleName: pgtype.Text{String: q.role, Valid: true}}, q.err
}

type panicDB struct{}

func (panicDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	panic("denial reached transaction")
}
func staffContext() context.Context {
	return context.WithValue(context.Background(), middleware.UserContextKey, &token.AccessClaims{StaffID: 7, RoleName: "SUPER_ADMIN"})
}

func TestManagementServicesDenyBeforeTargetIO(t *testing.T) {
	dbErr := errors.New("fixture lookup failure")
	for _, tc := range []struct {
		name, role      string
		lookup, want    error
		missing, active bool
	}{
		{name: "missing", missing: true, want: authz.ErrUnauthenticated},
		{name: "viewer", role: "VIEWER", active: true, want: authz.ErrForbidden},
		{name: "operator", role: "OPERATOR", active: true, want: authz.ErrForbidden},
		{name: "admin", role: "ADMIN", active: true, want: authz.ErrForbidden},
		{name: "unknown", role: "CUSTOM", active: true, want: authz.ErrForbidden},
		{name: "inactive", role: "SUPER_ADMIN", want: authz.ErrUnauthenticated},
		{name: "deleted", lookup: pgx.ErrNoRows, want: authz.ErrUnauthenticated},
		{name: "failure", lookup: dbErr, want: dbErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewService(principalQueries{role: tc.role, err: tc.lookup, active: tc.active}, panicDB{})
			ctx := staffContext()
			if tc.missing {
				ctx = context.Background()
			}
			id := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
			operations := map[string]func() error{
				"list":         func() error { _, err := s.ListManaged(ctx, "", 1, 20); return err },
				"detail":       func() error { _, err := s.GetManaged(ctx, id); return err },
				"create":       func() error { _, err := s.Create(ctx, "mobile-a", "A"); return err },
				"rename":       func() error { _, err := s.Rename(ctx, id, "A", Preconditions{IfMatch: `"tag"`}); return err },
				"grants list":  func() error { _, err := s.ListGrants(ctx, id, 1, 20); return err },
				"grant detail": func() error { _, err := s.GetGrant(ctx, id, 7); return err },
				"enable":       func() error { _, err := s.EnableGrant(ctx, id, 7, Preconditions{IfNoneMatch: "*"}); return err },
				"revoke":       func() error { return s.RevokeGrant(ctx, id, 7, Preconditions{IfMatch: `"tag"`}) },
			}
			for name, op := range operations {
				t.Run(name, func(t *testing.T) { require.ErrorIs(t, op(), tc.want) })
			}
		})
	}
	require.Panics(t, func() { NewService(nil, panicDB{}) })
	require.Panics(t, func() { NewService(principalQueries{}, nil) })
}

// The fake tx exposes only transaction methods. Callback tests exercise the
// retry boundary itself; real SQL and audit atomicity use PostgreSQL tests.
type retryTx struct {
	pgx.Tx
	commitErr          error
	commits, rollbacks int
}

func (t *retryTx) Commit(context.Context) error   { t.commits++; return t.commitErr }
func (t *retryTx) Rollback(context.Context) error { t.rollbacks++; return nil }

type retryDB struct {
	attempts []*retryTx
	begins   int
}

func (d *retryDB) BeginTx(_ context.Context, o pgx.TxOptions) (pgx.Tx, error) {
	if o.IsoLevel != pgx.ReadCommitted {
		panic("wrong isolation")
	}
	v := d.attempts[d.begins]
	d.begins++
	return v, nil
}
func TestWholeTransactionRetryPolicy(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		t.Run(code, func(t *testing.T) {
			txs := []*retryTx{{}, {}, {}}
			db := &retryDB{attempts: txs}
			s := NewService(principalQueries{}, db)
			calls := 0
			var firstDeadline time.Time
			err := s.write(context.Background(), func(ctx context.Context, _ *sqlc.Queries) error {
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
				if calls == 0 {
					firstDeadline = deadline
				}
				require.Equal(t, firstDeadline, deadline, "retries must share the original budget")
				calls++
				if calls < 3 {
					return &pgconn.PgError{Code: code}
				}
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, 3, calls)
			require.Equal(t, 3, db.begins)
			for _, tx := range txs {
				require.Equal(t, 1, tx.rollbacks)
			}
			require.Equal(t, 1, txs[2].commits)
		})
	}
	t.Run("bounded exhaustion", func(t *testing.T) {
		db := &retryDB{attempts: []*retryTx{{}, {}, {}}}
		s := NewService(principalQueries{}, db)
		err := s.write(context.Background(), func(context.Context, *sqlc.Queries) error { return &pgconn.PgError{Code: "40001"} })
		require.Error(t, err)
		require.Equal(t, 3, db.begins)
	})
	t.Run("serialization at commit retries full callback", func(t *testing.T) {
		first := &retryTx{commitErr: &pgconn.PgError{Code: "40001"}}
		second := &retryTx{}
		db := &retryDB{attempts: []*retryTx{first, second}}
		s := NewService(principalQueries{}, db)
		calls := 0
		require.NoError(t, s.write(context.Background(), func(context.Context, *sqlc.Queries) error { calls++; return nil }))
		require.Equal(t, 2, calls)
		require.Equal(t, 2, db.begins)
		require.Equal(t, 1, first.rollbacks)
		require.Equal(t, 1, second.commits)
	})

	t.Run("ambiguous commit not retried", func(t *testing.T) {
		uncertain := errors.New("fixture lost commit acknowledgement")
		tx := &retryTx{commitErr: uncertain}
		db := &retryDB{attempts: []*retryTx{tx}}
		s := NewService(principalQueries{}, db)
		err := s.write(context.Background(), func(context.Context, *sqlc.Queries) error { return nil })
		require.ErrorIs(t, err, uncertain)
		require.Equal(t, 1, db.begins)
	})
	t.Run("cancellation stops retry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		db := &retryDB{attempts: []*retryTx{{}}}
		s := NewService(principalQueries{}, db)
		err := s.write(ctx, func(context.Context, *sqlc.Queries) error { cancel(); return &pgconn.PgError{Code: "40001"} })
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, db.begins)
	})
}

func TestNameAndPreconditionContract(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
	}{
		{" Name ", true}, {strings.Repeat("é", 64), true}, {strings.Repeat("é", 65), false},
		{"\nName", false}, {"Name\n", false}, {"\u2003", false}, {"\x00", false}, {string([]byte{255}), false},
	} {
		_, err := cleanName(tc.value)
		require.Equal(t, tc.ok, err == nil)
	}
	for _, tc := range []struct {
		c      Preconditions
		create bool
		want   error
	}{
		{Preconditions{}, false, ErrPreconditionRequired},
		{Preconditions{IfMatch: `"tag"`}, false, nil},
		{Preconditions{IfNoneMatch: "*"}, true, nil},
		{Preconditions{IfNoneMatch: "*"}, false, ErrInvalidPrecondition},
		{Preconditions{IfMatch: `"tag"`, IfNoneMatch: "*"}, true, ErrInvalidPrecondition},
		{Preconditions{IfMatch: `W/"tag"`}, false, ErrInvalidPrecondition},
		{Preconditions{IfMatch: `"a", "b"`}, false, ErrInvalidPrecondition},
	} {
		require.ErrorIs(t, tc.c.validate(tc.create), tc.want)
	}
}
