//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/profile"
	"hello/internal/features/staff"
	"hello/internal/middleware"
	"hello/internal/token"
)

type staffFixtureTransaction struct{ pgx.Tx }

func (f staffFixtureTransaction) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	return f.Begin(ctx)
}

func TestStaffMutationSerializesRecoveryAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	pool := acceptanceFixture(t, ctx)
	q := sqlc.New(pool)
	tokens := token.NewTokenManager("fixture-access-not-production", "fixture-refresh-not-production")
	jwt := middleware.NewJWTMiddleware(tokens, repository.NewStaffUserRepository(q))
	api := middleware.NewAPIKeyMiddleware("fixture-api-key")
	mux := http.NewServeMux()
	staff.NewStaffModule(q, pool, api.Use, jwt.Use, middleware.NewAuditMiddleware().Use).RegisterRoutes(mux)
	profile.NewProfileModule(q, pool, api.Use, jwt.Use).RegisterRoutes(mux)
	hash, err := bcrypt.GenerateFromPassword([]byte("actor-password"), bcrypt.MinCost)
	require.NoError(t, err)
	type actor struct {
		id     int64
		access string
	}
	seed := func(role, name string) actor {
		var a actor
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,$2::text,$2::text || '@fixture.invalid',$3 FROM roles WHERE name=$1 RETURNING id`, role, name, string(hash)).Scan(&a.id))
		a.access, err = fixtureAccess(ctx, q, tokens, a.id, role, "")
		require.NoError(t, err)
		return a
	}
	request := func(a actor, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer "+a.access)
		r.Header.Set("X-API-Key", "fixture-api-key")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	first, second := seed("SUPER_ADMIN", "first"), seed("SUPER_ADMIN", "second")
	path := func(a actor) string { return fmt.Sprintf("/api/v1/staff/%d", a.id) }
	require.Equal(t, 400, request(first, "PATCH", path(first)+"/status", `{}`).Code)
	require.Equal(t, 400, request(first, "PATCH", path(first)+"/status", `{"is_active":null}`).Code)
	// Both eligible admins try to remove their own authority at the same time.
	var results [2]int
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Go(func() { <-start; results[0] = request(first, "PATCH", path(first), `{"role":"VIEWER"}`).Code })
	wg.Go(func() {
		<-start
		results[1] = request(second, "PATCH", path(second)+"/status", `{"is_active":false}`).Code
	})
	close(start)
	wg.Wait()
	require.ElementsMatch(t, []int{200, 409}, results[:])
	keeper := first
	if results[0] == 200 {
		keeper = second
	}
	var admins, audits int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM staff_users s JOIN roles r ON r.id=s.role_id WHERE s.is_active AND r.name='SUPER_ADMIN'`).Scan(&admins))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE entity_type='staff_user'`).Scan(&audits))
	require.Equal(t, 1, admins)
	require.Equal(t, 1, audits)
	for _, op := range []struct{ suffix, body string }{{"", "{\"role\":\"ADMIN\"}"}, {"/status", "{\"is_active\":false}"}} {
		require.Equal(t, 409, request(keeper, "PATCH", path(keeper)+op.suffix, op.body).Code)
	}
	require.Equal(t, 403, request(keeper, "DELETE", path(keeper), "").Code)
	target := seed("OPERATOR", "unique-search")
	var response struct {
		Data struct {
			Total int64 `json:"total"`
		}
	}
	w := request(keeper, "GET", "/api/v1/staff?search=unique-search", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.EqualValues(t, 1, response.Data.Total)

	// Hold a demotion uncommitted while the old profile token passes middleware.
	// The profile write must recheck its generation after acquiring the user lock.
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, `UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1`, target.id)
	require.NoError(t, err)
	answered := make(chan int, 1)
	go func() { answered <- request(target, "PATCH", "/api/v1/profile", `{"name":"stale"}`).Code }()
	require.Eventually(t, func() bool {
		var blocked bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%LockStaffMutationUsers%')`).Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit(ctx))
	require.Equal(t, 401, <-answered)
	row, err := q.GetStaffUserByID(ctx, target.id)
	require.NoError(t, err)
	require.Equal(t, "unique-search", row.Name)
	role, err := q.GetRoleByID(ctx, row.RoleID)
	require.NoError(t, err)
	require.Equal(t, "VIEWER", role.Name)
	target.access, err = fixtureAccess(ctx, q, tokens, target.id, "VIEWER", "")
	require.NoError(t, err)
	require.Equal(t, 200, request(target, "PATCH", "/api/v1/profile", `{"name":"updated"}`).Code)
	require.Equal(t, 409, request(target, "PATCH", "/api/v1/profile", `{"email":"first@fixture.invalid"}`).Code)
	// Failed audit must roll back the account change and preserve the same actor session.
	_, err = pool.Exec(ctx, `ALTER TABLE audit_logs ADD CONSTRAINT reject_fixture_change CHECK(action <> 'STAFF_UPDATE') NOT VALID`)
	require.NoError(t, err)
	require.Equal(t, 500, request(keeper, "PATCH", path(target), `{"name":"unwritten"}`).Code)
	row, err = q.GetStaffUserByID(ctx, target.id)
	require.NoError(t, err)
	require.Equal(t, "updated", row.Name)
	// Two simultaneous password changes prove the original password; exactly one may commit.
	start = make(chan struct{})
	passwords := []string{"first-new-password", "second-new-password"}
	for i, password := range passwords {
		wg.Go(func() {
			<-start
			results[i] = request(target, "PATCH", "/api/v1/profile/password", fmt.Sprintf(`{"current_password":"actor-password","new_password":%q}`, password)).Code
		})
	}
	close(start)
	wg.Wait()
	require.ElementsMatch(t, []int{200, 401}, results[:])
	row, err = q.GetStaffUserByID(ctx, target.id)
	require.NoError(t, err)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(passwords[slices.Index(results[:], 200)])))

	boundaryPassword := strings.Repeat("é", 36)
	hash, err = bcrypt.GenerateFromPassword([]byte(boundaryPassword), bcrypt.MinCost)
	require.NoError(t, err)
	boundary := seed("SUPER_ADMIN", "bcrypt-boundary")
	for _, tc := range []struct {
		name, path, field string
		reject            int
		targetID          int64
	}{
		{name: "admin reset", path: path(target) + "/password", field: "password", reject: http.StatusForbidden, targetID: target.id},
		{name: "profile change", path: "/api/v1/profile/password", field: "new_password", reject: http.StatusUnauthorized, targetID: boundary.id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := func(proof string) string {
				return fmt.Sprintf(`{"current_password":%q,%q:"new-fixture-password"}`, proof, tc.field)
			}
			before, err := q.GetStaffUserByID(ctx, tc.targetID)
			require.NoError(t, err)
			require.Equal(t, tc.reject, request(boundary, "PATCH", tc.path, body(boundaryPassword+"x")).Code)
			after, err := q.GetStaffUserByID(ctx, tc.targetID)
			require.NoError(t, err)
			require.Equal(t, before.PasswordHash, after.PasswordHash)
			require.Equal(t, before.AuthEpoch, after.AuthEpoch)
			require.Equal(t, http.StatusOK, request(boundary, "PATCH", tc.path, body(boundaryPassword)).Code)
		})
	}
}
