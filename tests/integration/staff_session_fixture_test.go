//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

// authz fixtures can vary the JWT's descriptive role independently of the DB.
// A missing or inactive actor deliberately gets an unregistered family so that
// endpoint tests exercise the real middleware rejection.
func fixtureStaffPair(ctx context.Context, q *sqlc.Queries, m *token.TokenManager, staffID int64, role, email string) (token.Pair, error) {
	f := token.NewFamily()
	p, err := m.Issue(staffID, role, email, f)
	if err != nil {
		return token.Pair{}, err
	}
	actor, err := q.GetStaffUserByID(ctx, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return token.Pair{}, err
	}
	if !actor.IsActive {
		return p, nil
	}
	n, err := q.OpenStaffSession(ctx, sqlc.OpenStaffSessionParams{SessionID: f.ID, StaffID: staffID, AuthEpoch: actor.AuthEpoch, RefreshDigest: p.RefreshDigest[:], ExpiresAt: pgtype.Timestamptz{Time: f.ExpiresAt, Valid: true}})
	if err != nil {
		return token.Pair{}, err
	}
	if n != 1 {
		return token.Pair{}, errors.New("active fixture session withheld")
	}
	return p, nil
}
func fixtureAccess(ctx context.Context, q *sqlc.Queries, m *token.TokenManager, staffID int64, role, email string) (string, error) {
	p, err := fixtureStaffPair(ctx, q, m, staffID, role, email)
	return p.AccessToken, err
}

func fixtureStaffContext(t *testing.T, ctx context.Context, q *sqlc.Queries, staffID int64) context.Context {
	t.Helper()
	manager := token.NewTokenManager("fixture-access", "fixture-refresh")
	raw, err := fixtureAccess(ctx, q, manager, staffID, "VIEWER", "")
	require.NoError(t, err)
	claims, err := manager.ParseAccessToken(raw)
	require.NoError(t, err)
	return context.WithValue(ctx, middleware.UserContextKey, claims)
}
