package staffmutation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

type mutationTx struct {
	pgx.Tx
	steps      []string
	before     func(string)
	failAt     string
	failure    error
	unexpired  bool
	committed  bool
	rolledBack bool
}

func (tx *mutationTx) step(name string) {
	tx.steps = append(tx.steps, name)
	if tx.before != nil {
		tx.before(name)
	}
}

func (tx *mutationTx) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	var name string
	var value any
	switch {
	case strings.Contains(query, "LockStaffAdministration"):
		name, value = "administration", true
	case strings.Contains(query, "LockStaffMutationLineage"):
		name, value = "lineage", token.NewFamily().ID
	case strings.Contains(query, "LockStaffMutationSession"):
		name, value = "session", token.NewFamily().ID
	case strings.Contains(query, "expires_at > clock_timestamp()") && !strings.Contains(query, "auth_epoch"):
		name, value = "expiry", tx.unexpired
	default:
		panic("unexpected query: " + query)
	}
	tx.step(name)
	if tx.failAt == name {
		return mutationRow{err: tx.failure}
	}
	return mutationRow{value: value}
}

func (tx *mutationTx) Query(_ context.Context, query string, _ ...any) (pgx.Rows, error) {
	if !strings.Contains(query, "LockStaffMutationUsers") {
		panic("unexpected query: " + query)
	}
	tx.step("users")
	if tx.failAt == "users" {
		return nil, tx.failure
	}
	return mutationRows{}, nil
}

func (tx *mutationTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *mutationTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}

type mutationRow struct {
	value any
	err   error
}

func (r mutationRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	switch p := dest[0].(type) {
	case *bool:
		*p = r.value.(bool)
	case *pgtype.UUID:
		*p = r.value.(pgtype.UUID)
	default:
		panic("unexpected scan destination")
	}
	return nil
}

type mutationRows struct{ pgx.Rows }

func (mutationRows) Next() bool { return false }
func (mutationRows) Close()     {}
func (mutationRows) Err() error { return nil }

type mutationDB struct {
	tx    *mutationTx
	begun bool
}

func (db *mutationDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	db.begun = true
	return db.tx, nil
}

func mutationClaims() *token.AccessClaims {
	return &token.AccessClaims{
		StaffID:   42,
		SessionID: token.NewFamily().ID.String(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}
}

func TestRunRejectsInvalidIdentityAndExpiry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*token.AccessClaims) *token.AccessClaims
	}{
		{name: "missing claims", change: func(*token.AccessClaims) *token.AccessClaims { return nil }},
		{name: "missing expiry", change: func(c *token.AccessClaims) *token.AccessClaims { c.ExpiresAt = nil; return c }},
		{name: "expired claims", change: func(c *token.AccessClaims) *token.AccessClaims {
			c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Second))
			return c
		}},
		{name: "invalid actor", change: func(c *token.AccessClaims) *token.AccessClaims { c.StaffID = 0; return c }},
		{name: "invalid session", change: func(c *token.AccessClaims) *token.AccessClaims { c.SessionID = "invalid"; return c }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &mutationDB{}
			ctx := context.WithValue(t.Context(), middleware.UserContextKey, tc.change(mutationClaims()))
			result, err := Run(ctx, db, 42, false, func(*sqlc.Queries) (string, error) {
				t.Fatal("work called without valid unexpired claims")
				return "unexpected", nil
			})
			require.ErrorIs(t, err, authz.ErrUnauthenticated)
			require.Empty(t, result)
			require.False(t, db.begun)
		})
	}
}

func TestRunExpiryAndRollback(t *testing.T) {
	failure := errors.New("fixture database failure")
	for _, tc := range []struct {
		name       string
		waitAt     string
		failAt     string
		failure    error
		workErr    error
		dbExpired  bool
		wantWork   bool
		wantErr    error
		wantCommit bool
	}{
		{name: "valid", wantWork: true, wantCommit: true},
		{name: "administration lock expiry", waitAt: "administration", wantErr: authz.ErrUnauthenticated},
		{name: "lineage lock expiry", waitAt: "lineage", wantErr: authz.ErrUnauthenticated},
		{name: "user lock expiry", waitAt: "users", wantErr: authz.ErrUnauthenticated},
		{name: "session lock expiry", waitAt: "session", wantErr: authz.ErrUnauthenticated},
		{name: "expiry during work", waitAt: "work", wantWork: true, wantErr: authz.ErrUnauthenticated},
		{name: "expiry during final database check", waitAt: "expiry", wantWork: true, wantErr: authz.ErrUnauthenticated},
		{name: "database session expiry", dbExpired: true, wantWork: true, wantErr: authz.ErrUnauthenticated},
		{name: "revoked session", failAt: "session", failure: pgx.ErrNoRows, wantErr: authz.ErrUnauthenticated},
		{name: "session query error", failAt: "session", failure: failure, wantErr: failure},
		{name: "expiry query error", failAt: "expiry", failure: failure, wantWork: true, wantErr: failure},
		{name: "work error", workErr: failure, wantWork: true, wantErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := mutationClaims()
			if tc.waitAt != "" {
				// Preserve subsecond expiry without changing the JWT package's global precision.
				claims.ExpiresAt = &jwt.NumericDate{Time: time.Now().Add(100 * time.Millisecond)}
			}
			expires := claims.ExpiresAt.Time
			tx := &mutationTx{unexpired: !tc.dbExpired, failAt: tc.failAt, failure: tc.failure}
			tx.before = func(step string) {
				if step == tc.waitAt {
					timer := time.NewTimer(time.Until(expires))
					defer timer.Stop()
					select {
					case <-timer.C:
					case <-t.Context().Done():
						t.Fatal("test canceled while waiting for expiry")
					}
				}
			}
			db := &mutationDB{tx: tx}
			ctx := context.WithValue(t.Context(), middleware.UserContextKey, claims)
			worked := false
			result, err := Run(ctx, db, 42, true, func(*sqlc.Queries) (string, error) {
				worked = true
				tx.step("work")
				return "changed", tc.workErr
			})
			if tc.wantErr == nil {
				require.NoError(t, err)
				require.Equal(t, "changed", result)
				require.Equal(t, []string{"administration", "lineage", "users", "session", "work", "expiry"}, tx.steps)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
				require.Empty(t, result)
				require.True(t, tx.rolledBack)
			}
			require.Equal(t, tc.wantWork, worked)
			require.Equal(t, tc.wantCommit, tx.committed)
		})
	}
}

func TestRunProfileTargetGuard(t *testing.T) {
	db := &mutationDB{}
	ctx := context.WithValue(t.Context(), middleware.UserContextKey, mutationClaims())
	_, err := Run(ctx, db, 43, false, func(*sqlc.Queries) (struct{}, error) {
		t.Fatal("work called for another profile")
		return struct{}{}, nil
	})
	require.ErrorIs(t, err, authz.ErrForbidden)
	require.False(t, db.begun)
}
