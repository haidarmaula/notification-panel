package installation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

type lifecycleDeadlineStore struct {
	pgx.Tx
	begin    func(context.Context) error
	query    func(context.Context) error
	rollback func(context.Context) error
}

func (s *lifecycleDeadlineStore) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	if s.begin != nil {
		if err := s.begin(ctx); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *lifecycleDeadlineStore) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	return lifecycleErrorRow{s.query(ctx)}
}

func (s *lifecycleDeadlineStore) Rollback(ctx context.Context) error {
	return s.rollback(ctx)
}

type lifecycleErrorRow struct{ err error }

func (r lifecycleErrorRow) Scan(...any) error { return r.err }

func TestLifecycleBoundsDatabaseWork(t *testing.T) {
	actor := Principal{ApplicationID: 1, UserID: 1}
	id := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	operations := []struct {
		name string
		run  func(context.Context, *Lifecycle) error
	}{
		{"enroll", func(ctx context.Context, s *Lifecycle) error {
			_, err := s.Enroll(ctx, actor, Route{}, "address")
			return err
		}},
		{"apply", func(ctx context.Context, s *Lifecycle) error {
			_, err := s.Apply(ctx, actor, Change{Operation: Logout})
			return err
		}},
		{"state", func(ctx context.Context, s *Lifecycle) error {
			_, err := s.ReadState(ctx, actor, id, "proof")
			return err
		}},
		{"invalidate", func(ctx context.Context, s *Lifecycle) error {
			_, err := s.Invalidate(ctx, actor.ApplicationID, id, 1, 1)
			return err
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			want := errors.New("query failed")
			var beginDeadline time.Time
			store := &lifecycleDeadlineStore{
				begin: func(ctx context.Context) error {
					var ok bool
					beginDeadline, ok = ctx.Deadline()
					require.True(t, ok, "pool acquisition must be bounded")
					require.WithinDuration(t, time.Now().Add(5*time.Second), beginDeadline, time.Second)
					return nil
				},
				query: func(ctx context.Context) error {
					deadline, ok := ctx.Deadline()
					require.True(t, ok, "SQL must receive the bounded context")
					require.Equal(t, beginDeadline, deadline)
					return want
				},
				rollback: func(context.Context) error { return nil },
			}
			require.ErrorIs(t, operation.run(context.Background(), &Lifecycle{db: store}), want)
		})
	}
}

func TestLifecyclePreservesCallerDeadlineAndDetachesRollback(t *testing.T) {
	type contextKey struct{}
	parent := context.WithValue(context.Background(), contextKey{}, "trace")
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	wantDeadline, _ := ctx.Deadline()
	want := errors.New("query failed")
	rolledBack := false
	store := &lifecycleDeadlineStore{
		begin: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.Equal(t, wantDeadline, deadline)
			return nil
		},
		query: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.Equal(t, wantDeadline, deadline)
			cancel()
			return want
		},
		rollback: func(ctx context.Context) error {
			rolledBack = true
			require.NoError(t, ctx.Err(), "cleanup must survive caller cancellation")
			deadline, ok := ctx.Deadline()
			require.True(t, ok, "cleanup must have its own deadline")
			require.WithinDuration(t, time.Now().Add(time.Second), deadline, 100*time.Millisecond)
			require.Equal(t, "trace", ctx.Value(contextKey{}))
			return nil
		},
	}
	_, err := (&Lifecycle{db: store}).Enroll(ctx, Principal{ApplicationID: 1, UserID: 1}, Route{}, "address")
	require.ErrorIs(t, err, want)
	require.True(t, rolledBack)
}

func TestLifecycleRetriesShareOperationDeadline(t *testing.T) {
	var deadline time.Time
	attempts := 0
	store := &lifecycleDeadlineStore{
		begin: func(ctx context.Context) error {
			got, ok := ctx.Deadline()
			require.True(t, ok)
			attempts++
			if attempts == 1 {
				deadline = got
			} else {
				require.Equal(t, deadline, got, "retries must not reset the budget")
			}
			return &pgconn.PgError{Code: "40001"}
		},
	}
	_, err := (&Lifecycle{db: store}).Enroll(context.Background(), Principal{ApplicationID: 1, UserID: 1}, Route{}, "address")
	require.Error(t, err)
	require.Equal(t, 3, attempts)
}
