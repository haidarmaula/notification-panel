//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"hello/internal/authz"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/staff"
	"hello/internal/middleware"
	"hello/internal/token"
)

func TestStaffAdministrationAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	q := sqlc.New(tx)
	hash, err := bcrypt.GenerateFromPassword([]byte("actor-password"), bcrypt.MinCost)
	require.NoError(t, err)
	seed := func(role string) int64 {
		var id int64
		require.NoError(t, tx.QueryRow(ctx, `INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Fixture',gen_random_uuid()::text || '@fixture.invalid',$2 FROM roles WHERE name=$1 RETURNING id`, role, string(hash)).Scan(&id))
		return id
	}
	repo := repository.NewStaffUserRepository(q)
	checker := authz.New(repo)
	tokens := token.NewTokenManager("fixture-access-not-production", "fixture-refresh-not-production")
	api := middleware.NewAPIKeyMiddleware("fixture-api-key")
	jwt := middleware.NewJWTMiddleware(tokens, repo)
	auditMW := middleware.NewAuditMiddleware()
	mux := http.NewServeMux()
	staff.NewStaffModule(q, api.Use, jwt.Use, auditMW.Use).RegisterRoutes(mux)
	sign := func(id int64, role string) string {
		value, err := fixtureAccess(ctx, q, tokens, id, role, "fixture@fixture.invalid")
		require.NoError(t, err)
		return value
	}
	request := func(handler http.Handler, method, path, body, access, omit string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		r.Header.Set("X-API-Key", "fixture-api-key")
		r.Header.Set("Authorization", "Bearer "+access)
		r.Header.Set("Content-Type", "application/json")
		if omit != "" {
			r.Header.Del(omit)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	snapshot := func() string {
		var value string
		require.NoError(t, tx.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(s ORDER BY id) FROM staff_users s),(SELECT jsonb_agg(a ORDER BY id) FROM audit_logs a))::text`).Scan(&value))
		return value
	}
	data, err := os.ReadFile("../../docs/contracts/staff-permissions.json")
	require.NoError(t, err)
	var contract struct {
		StaffRoutes []struct{ Method, Path, Permission string } `json:"staff_routes"`
		Permissions map[string][]string
		Roles       []string
	}
	require.NoError(t, json.Unmarshal(data, &contract))
	require.Len(t, contract.StaffRoutes, 7)
	for _, role := range contract.Roles {
		t.Run(role, func(t *testing.T) {
			actor := seed(role)
			for i, route := range contract.StaffRoutes {
				t.Run(route.Method+" "+route.Path, func(t *testing.T) {
					target := seed("VIEWER")
					path := strings.ReplaceAll(route.Path, "{id}", strconv.FormatInt(target, 10))
					body := fmt.Sprintf(`{"role":"ADMIN","name":"Changed","email":"changed-%d-%d@fixture.invalid","password":"new-password","current_password":"actor-password","is_active":false}`, actor, i)
					claim := "SUPER_ADMIN"
					if role == "SUPER_ADMIN" {
						claim = "VIEWER"
					}
					before := snapshot()
					var auditsBefore int64
					require.NoError(t, tx.QueryRow(ctx, "SELECT count(*) FROM audit_logs").Scan(&auditsBefore))
					w := request(mux, route.Method, path, body, sign(actor, claim), "")
					want := 403
					if role == "SUPER_ADMIN" {
						want = 200
						if route.Method == "POST" {
							want = 201
						}
					}
					require.Equal(t, []string{"SUPER_ADMIN"}, contract.Permissions[route.Permission])
					require.Equal(t, want, w.Code, w.Body.String())
					require.NotContains(t, w.Body.String(), "password_hash")
					require.NotContains(t, w.Body.String(), string(hash))
					if role == "SUPER_ADMIN" && route.Method != "GET" {
						var auditsAfter int64
						require.NoError(t, tx.QueryRow(ctx, "SELECT count(*) FROM audit_logs").Scan(&auditsAfter))
						require.Equal(t, auditsBefore+1, auditsAfter, "successful mutation must persist a fixture audit")
					}

					if want == 403 {
						require.Equal(t, before, snapshot(), "denial mutated staff/audit")
					}
				})
			}
		})
	}
	t.Run("persisted snapshots contain only allowed fields", func(t *testing.T) {
		rows, err := tx.Query(ctx, "SELECT action,before_json,after_json,entity_id,actor_user_id FROM audit_logs WHERE entity_type='staff_user' ORDER BY id")
		require.NoError(t, err)
		defer rows.Close()
		actions := map[string]bool{}
		allowed := map[string]bool{"ID": true, "RoleID": true, "RoleName": true, "Name": true, "Email": true, "IsActive": true, "CreatedAt": true, "UpdatedAt": true}
		for rows.Next() {
			var action string
			var before, after []byte
			var entity, actor int64
			require.NoError(t, rows.Scan(&action, &before, &after, &entity, &actor))
			actions[action] = true
			require.Positive(t, entity)
			require.Positive(t, actor)
			if action == "STAFF_UPDATE_PASSWORD" {
				require.Nil(t, before)
				require.Nil(t, after)
				continue
			}
			for _, data := range [][]byte{before, after} {
				if data == nil {
					continue
				}
				var payload map[string]any
				require.NoError(t, json.Unmarshal(data, &payload))
				for key := range payload {
					require.True(t, allowed[key], "unexpected audit key: %s", key)
				}
				require.Equal(t, float64(entity), payload["ID"])
				require.Contains(t, payload, "Name")
				require.Contains(t, payload, "RoleID")
				require.NotContains(t, string(data), string(hash))
				require.NotContains(t, string(data), "password")
			}
		}
		require.NoError(t, rows.Err())
		for _, action := range []string{"STAFF_CREATE", "STAFF_UPDATE", "STAFF_UPDATE_STATUS", "STAFF_UPDATE_PASSWORD", "STAFF_DELETE"} {
			require.True(t, actions[action], "missing audit action: %s", action)
		}
	})

	t.Run("credentials and account lifecycle", func(t *testing.T) {
		actor := seed("SUPER_ADMIN")
		access := sign(actor, "SUPER_ADMIN")
		for _, header := range []string{"X-API-Key", "Authorization"} {
			before := snapshot()
			w := request(mux, "GET", "/api/v1/staff", "", access, header)
			require.Equal(t, 401, w.Code)
			require.Equal(t, before, snapshot())
		}
		_, err = tx.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='ADMIN') WHERE id=$1", actor)
		require.NoError(t, err)
		before := snapshot()
		w := request(mux, "GET", "/api/v1/staff", "", access, "")
		require.Equal(t, 401, w.Code)
		require.Equal(t, before, snapshot())
		_, err = tx.Exec(ctx, "UPDATE staff_users SET is_active=false WHERE id=$1", actor)
		require.NoError(t, err)
		w = request(mux, "GET", "/api/v1/staff", "", access, "")
		require.Equal(t, 401, w.Code)
		_, err = tx.Exec(ctx, "DELETE FROM staff_users WHERE id=$1", actor)
		require.NoError(t, err)
		w = request(mux, "GET", "/api/v1/staff", "", access, "")
		require.Equal(t, 401, w.Code)
	})
	t.Run("password confirmation and self delete", func(t *testing.T) {
		actor, target := seed("SUPER_ADMIN"), seed("VIEWER")
		access := sign(actor, "VIEWER")
		before := snapshot()
		w := request(mux, "PATCH", fmt.Sprintf("/api/v1/staff/%d/password", target), `{"current_password":"wrong-password","password":"new-password"}`, access, "")
		require.Equal(t, 403, w.Code)
		require.Equal(t, before, snapshot())
		w = request(mux, "DELETE", fmt.Sprintf("/api/v1/staff/%d", actor), "", access, "")
		require.Equal(t, 403, w.Code)
		require.Equal(t, before, snapshot())
	})
	t.Run("self demotion and deactivation keep response and audit", func(t *testing.T) {
		for _, operation := range []struct{ suffix, body string }{{"", `{"role":"VIEWER"}`}, {"/status", `{"is_active":false}`}} {
			actor := seed("SUPER_ADMIN")
			access := sign(actor, "VIEWER")
			var before int64
			require.NoError(t, tx.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE actor_user_id=$1", actor).Scan(&before))
			w := request(mux, "PATCH", fmt.Sprintf("/api/v1/staff/%d%s", actor, operation.suffix), operation.body, access, "")
			require.Equal(t, 200, w.Code, w.Body.String())
			var after int64
			require.NoError(t, tx.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE actor_user_id=$1", actor).Scan(&after))
			require.Equal(t, before+1, after)
			w = request(mux, "GET", "/api/v1/staff", "", access, "")
			require.Equal(t, 401, w.Code)
		}
	})
	t.Run("demotion after route check is denied by service", func(t *testing.T) {
		actor := seed("SUPER_ADMIN")
		hook := checkerFunc(func(c context.Context, p ...authz.Permission) (authz.Principal, error) {
			principal, err := checker.Require(c, p...)
			if err == nil {
				_, err = tx.Exec(c, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1", actor)
			}
			return principal, err
		})
		// Nil business repositories prove the service denies before target I/O.
		handler := staff.NewStaffHandler(staff.NewStaffService(nil, nil, nil, checker))
		guarded := middleware.Chain(api.Use, jwt.Use)(authz.Guard(hook, authz.StaffManage, handler.Create))
		var countBefore int64
		require.NoError(t, tx.QueryRow(ctx, "SELECT count(*) FROM audit_logs").Scan(&countBefore))
		w := request(guarded, "POST", "/api/v1/staff", `{"role":"ADMIN","name":"Denied","email":"denied@fixture.invalid","password":"new-password"}`, sign(actor, "SUPER_ADMIN"), "")
		require.Equal(t, 403, w.Code, w.Body.String())
		var countAfter int64
		require.NoError(t, tx.QueryRow(ctx, "SELECT count(*) FROM audit_logs").Scan(&countAfter))
		require.Equal(t, countBefore, countAfter)
	})
}

// Pool writes commit before each new HTTP request; the same signed JWT is reused.
func TestCommittedStaffAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	var id int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Committed staff',gen_random_uuid()::text || '@fixture.invalid','fixture-only' FROM roles WHERE name='SUPER_ADMIN' RETURNING id`).Scan(&id))
	defer func() {
		_, err := pool.Exec(context.Background(), "DELETE FROM staff_users WHERE id=$1", id)
		require.NoError(t, err)
	}()
	q := sqlc.New(pool)
	repo := repository.NewStaffUserRepository(q)
	tokens := token.NewTokenManager("fixture-access-not-production", "fixture-refresh-not-production")
	api := middleware.NewAPIKeyMiddleware("fixture-api-key")
	jwt := middleware.NewJWTMiddleware(tokens, repo)
	mux := http.NewServeMux()
	staff.NewStaffModule(q, api.Use, jwt.Use).RegisterRoutes(mux)
	access, err := fixtureAccess(ctx, q, tokens, id, "SUPER_ADMIN", "")
	require.NoError(t, err)
	claims, err := tokens.ParseAccessToken(access)
	require.NoError(t, err)
	defer func() {
		_, err := pool.Exec(context.Background(), "DELETE FROM staff_sessions WHERE id=$1::uuid", claims.SessionID)
		require.NoError(t, err)
	}()
	request := func(want int) {
		r := httptest.NewRequest("GET", "/api/v1/staff", nil).WithContext(ctx)
		r.Header.Set("X-API-Key", "fixture-api-key")
		r.Header.Set("Authorization", "Bearer "+access)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		require.Equal(t, want, w.Code, w.Body.String())
	}
	request(200)
	_, err = pool.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1", id)
	require.NoError(t, err)
	request(401)
	_, err = pool.Exec(ctx, "UPDATE staff_users SET is_active=false WHERE id=$1", id)
	require.NoError(t, err)
	request(401)
	_, err = pool.Exec(ctx, "DELETE FROM staff_users WHERE id=$1", id)
	require.NoError(t, err)
	request(401)
}
