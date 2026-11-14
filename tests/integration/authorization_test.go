//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/authz"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/staff"
	"hello/internal/middleware"
	"hello/internal/token"
)

type checkerFunc func(context.Context, ...authz.Permission) (authz.Principal, error)

func (f checkerFunc) Require(ctx context.Context, p ...authz.Permission) (authz.Principal, error) {
	return f(ctx, p...)
}

func TestCommittedAuthorityChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	var id int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO staff_users(role_id,name,email,password_hash)
  SELECT id,'Committed fixture','committed-authz@fixture.invalid','fixture' FROM roles WHERE name='SUPER_ADMIN' RETURNING staff_users.id`).Scan(&id))
	defer func() {
		_, err := pool.Exec(context.Background(), "DELETE FROM staff_users WHERE id=$1", id)
		require.NoError(t, err)
	}()
	q := sqlc.New(pool)
	checker := authz.New(repository.NewStaffUserRepository(q))
	tokens := token.NewTokenManager("fixture-access-key-not-production", "fixture-refresh-key-not-production")
	access, err := fixtureAccess(ctx, q, tokens, id, "SUPER_ADMIN", "")
	require.NoError(t, err)
	claimsContext := context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: id, RoleName: "SUPER_ADMIN"})
	_, err = checker.Require(claimsContext, authz.StaffManage)
	require.NoError(t, err, "initial authority comes from the current database role")

	// The stale SUPER_ADMIN claim must not override the committed database role.
	_, err = pool.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1", id)
	require.NoError(t, err)
	_, err = checker.Require(claimsContext, authz.StaffManage)
	require.ErrorIs(t, err, authz.ErrForbidden)
	_, err = pool.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='SUPER_ADMIN') WHERE id=$1", id)
	require.NoError(t, err)
	access, err = fixtureAccess(ctx, q, tokens, id, "SUPER_ADMIN", "")
	require.NoError(t, err, "mint a fresh active session after the role change")

	// The active staff route and service each check live authority. Simulate a
	// role change after route authorization; the service check must deny before
	// it accesses mutation dependencies.
	hook := checkerFunc(func(c context.Context, p ...authz.Permission) (authz.Principal, error) {
		principal, err := checker.Require(c, p...)
		if err == nil {
			_, err = pool.Exec(c, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1", id)
		}
		return principal, err
	})
	api := middleware.NewAPIKeyMiddleware("fixture-api-key")
	jwt := middleware.NewJWTMiddleware(tokens, repository.NewStaffUserRepository(q))
	service := staff.NewStaffService(nil, nil, checker)
	handler := staff.NewStaffHandler(service)
	guarded := middleware.Chain(api.Use, jwt.Use)(authz.Guard(hook, authz.StaffManage, handler.Create))
	r := httptest.NewRequest(http.MethodPost, "/api/v1/staff", strings.NewReader(`{"role":"ADMIN","name":"Denied","email":"denied@fixture.invalid","password":"new-password"}`)).WithContext(ctx)
	r.Header.Set("X-API-Key", "fixture-api-key")
	r.Header.Set("Authorization", "Bearer "+access)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	guarded.ServeHTTP(w, r)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	var created int64
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM staff_users WHERE email='denied@fixture.invalid'").Scan(&created))
	require.Zero(t, created, "service-level authority denial must prevent mutation")

	_, err = pool.Exec(ctx, "UPDATE staff_users SET is_active=false WHERE id=$1", id)
	require.NoError(t, err)
	_, err = checker.Require(claimsContext, authz.StaffManage)
	require.ErrorIs(t, err, authz.ErrUnauthenticated)
}
