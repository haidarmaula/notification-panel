//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/applications"
	"hello/internal/middleware"
	"hello/internal/token"
)

// Exercise the real module, API-key/JWT/current DB guards, services, and SQL.
// Only PROVISIONING metadata is seeded; legacy business tables stay untouched.
func TestApplicationHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	q := sqlc.New(pool)
	service := applications.NewService(q, pool)
	ids := []int64{}
	keys := []string{"m03-http-a", "m03-http-b", "m03-http-created"}
	defer func() {
		_, e := pool.Exec(context.Background(), "DELETE FROM audit_logs WHERE actor_user_id=ANY($1)", ids)
		require.NoError(t, e)
		_, e = pool.Exec(context.Background(), "DELETE FROM application_staff_grants WHERE application_id IN(SELECT id FROM applications WHERE key=ANY($1))", keys)
		require.NoError(t, e)
		_, e = pool.Exec(context.Background(), "DELETE FROM applications WHERE key=ANY($1)", keys)
		require.NoError(t, e)
		_, e = pool.Exec(context.Background(), "DELETE FROM staff_users WHERE id=ANY($1)", ids)
		require.NoError(t, e)
	}()
	manager := token.NewTokenManager("http-fixture-access-secret", "http-fixture-refresh-secret")
	actors := map[string]int64{}
	tokens := map[string]string{}
	for _, role := range []string{"VIEWER", "OPERATOR", "ADMIN", "SUPER_ADMIN"} {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'HTTP fixture',gen_random_uuid()::text || '@fixture.invalid','private-fixture-hash' FROM roles WHERE name=$1 RETURNING id`, role).Scan(&id))
		ids = append(ids, id)
		actors[role] = id
		// Deliberately stale role claims: DB role alone controls permission.
		claim := "SUPER_ADMIN"
		if role == "SUPER_ADMIN" {
			claim = "VIEWER"
		}
		tokens[role], err = manager.GenerateAccessToken(id, claim, "")
		require.NoError(t, err)
	}
	super := context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: actors["SUPER_ADMIN"]})
	a, err := service.Create(super, keys[0], "Aplikasi A")
	require.NoError(t, err)
	b, err := service.Create(super, keys[1], "Aplikasi B")
	require.NoError(t, err)
	mux := http.NewServeMux()
	applications.NewModule(q, pool, middleware.NewAPIKeyMiddleware("registry-fixture-key").Use, middleware.NewJWTMiddleware(manager, repository.NewStaffUserRepository(q)).Use, middleware.NewAuditMiddleware().Use).RegisterRoutes(mux)
	request := func(t *testing.T, role, method, path, body string, headers map[string]string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r = r.WithContext(ctx)
		r.Header.Set("X-API-Key", "registry-fixture-key")
		if role != "" {
			r.Header.Set("Authorization", "Bearer "+tokens[role])
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		require.Equal(t, want, w.Code, "%s %s: %s", method, path, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		if want == 204 {
			require.Empty(t, w.Body.String())
		} else {
			var envelope map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			require.Equal(t, want < 400, envelope["success"])
		}
		require.NotContains(t, w.Body.String(), "private-fixture-hash")
		return w
	}
	data := func(w *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var v struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
		return v.Data
	}
	admin := "/api/v1/admin/apps/" + a.PublicID.String()
	public := "/api/v1/apps/" + a.PublicID.String()
	target := strconv.FormatInt(actors["VIEWER"], 10)
	grantPath := admin + "/staff-grants/" + target
	snapshot := func() string {
		var s string
		require.NoError(t, pool.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(a ORDER BY id) FROM applications a),(SELECT jsonb_agg(g ORDER BY application_id,staff_id) FROM application_staff_grants g),(SELECT jsonb_agg(l ORDER BY id) FROM audit_logs l))::text`).Scan(&s))
		return s
	}

	t.Run("authentication and management matrix", func(t *testing.T) {
		routes := []struct{ method, path, body string }{
			{"GET", "/api/v1/admin/apps", ""}, {"POST", "/api/v1/admin/apps", `{}`}, {"GET", admin, ""}, {"PATCH", admin, `{}`},
			{"GET", admin + "/staff-grants", ""}, {"GET", grantPath, ""}, {"PUT", grantPath, `{}`}, {"DELETE", grantPath, ""},
		}
		before := snapshot()
		for _, route := range routes {
			request(t, "", route.method, route.path, route.body, nil, 401)
			request(t, "SUPER_ADMIN", route.method, route.path, route.body, map[string]string{"X-API-Key": "wrong"}, 401)
			for _, role := range []string{"VIEWER", "OPERATOR", "ADMIN"} {
				request(t, role, route.method, route.path, route.body, nil, 403)
			}
		}
		for _, path := range []string{"/api/v1/apps", public} {
			request(t, "", "GET", path, "", nil, 401)
		}
		require.Equal(t, before, snapshot())
	})
	t.Run("explicit memberships for every role including super", func(t *testing.T) {
		// Global super registry access does not grant application access.
		request(t, "SUPER_ADMIN", "GET", public, "", nil, 404)
		request(t, "SUPER_ADMIN", "GET", public+"/users", "", nil, 404)
		for _, role := range []string{"VIEWER", "OPERATOR", "ADMIN", "SUPER_ADMIN"} {
			_, e := service.EnableGrant(super, a.PublicID, actors[role], applications.Preconditions{IfNoneMatch: "*"})
			require.NoError(t, e)
			w := request(t, role, "GET", public, "", nil, 200)
			v := data(w)
			require.Len(t, v, 7)
			require.Equal(t, a.PublicID.String(), v["id"])
			require.Equal(t, "1", v["revision"])
			require.Equal(t, "PROVISIONING", v["status"])
			for _, suffix := range []string{"/users", "/users/9", "/users/9/devices"} {
				request(t, role, "GET", public+suffix, "", nil, 409)
			}
			require.Equal(t, applications.ApplicationETag(a), w.Header().Get("ETag"))
			v = data(request(t, role, "GET", "/api/v1/apps", "", nil, 200))
			require.Equal(t, float64(1), v["total"])
			require.Len(t, v["items"], 1)
			unknown := request(t, role, "GET", "/api/v1/apps/00000000-0000-0000-0000-000000000001", "", nil, 404)
			foreign := request(t, role, "GET", "/api/v1/apps/"+b.PublicID.String(), "", nil, 404)
			require.Equal(t, unknown.Body.String(), foreign.Body.String())
		}
	})
	t.Run("management successes and CAS lifecycle", func(t *testing.T) {
		w := request(t, "SUPER_ADMIN", "POST", "/api/v1/admin/apps", `{"key":"m03-http-created","name":"  New project  "}`, nil, 201)
		v := data(w)
		require.Equal(t, "New project", v["name"])
		require.Equal(t, "1", v["revision"])
		require.Equal(t, "/api/v1/admin/apps/"+v["id"].(string), w.Header().Get("Location"))
		require.NotEmpty(t, w.Header().Get("ETag"))
		request(t, "SUPER_ADMIN", "GET", "/api/v1/apps/"+v["id"].(string), "", nil, 404)
		request(t, "SUPER_ADMIN", "POST", "/api/v1/admin/apps", `{"key":"m03-http-created","name":"Duplicate"}`, nil, 409)
		v = data(request(t, "SUPER_ADMIN", "GET", "/api/v1/admin/apps?key=m03-http-created", "", nil, 200))
		require.Equal(t, float64(1), v["total"])
		v = data(request(t, "SUPER_ADMIN", "GET", "/api/v1/admin/apps?key=m03-http-created&page=1000", "", nil, 200))
		require.Equal(t, float64(1), v["total"])
		require.Equal(t, []any{}, v["items"])
		first := request(t, "SUPER_ADMIN", "GET", admin, "", nil, 200).Header().Get("ETag")
		before := snapshot()
		w = request(t, "SUPER_ADMIN", "PATCH", admin, `{"name":"Aplikasi A"}`, map[string]string{"If-Match": first}, 200)
		require.Equal(t, first, w.Header().Get("ETag"))
		require.Equal(t, before, snapshot())
		w = request(t, "SUPER_ADMIN", "PATCH", admin, `{"name":"Renamed"}`, map[string]string{"If-Match": first}, 200)
		require.NotEqual(t, first, w.Header().Get("ETag"))
		require.Equal(t, "2", data(w)["revision"])
		request(t, "SUPER_ADMIN", "PATCH", admin, `{"name":"Stale"}`, map[string]string{"If-Match": first}, 412)
		g := request(t, "SUPER_ADMIN", "GET", grantPath, "", nil, 200)
		v = data(g)
		require.Len(t, v, 6)
		require.Equal(t, a.PublicID.String(), v["application_id"])
		require.Equal(t, target, v["staff_id"])
		etag := g.Header().Get("ETag")
		before = snapshot()
		request(t, "SUPER_ADMIN", "PUT", grantPath, `{"is_active":true}`, map[string]string{"If-Match": etag}, 200)
		require.Equal(t, before, snapshot())
		request(t, "SUPER_ADMIN", "DELETE", grantPath, "", map[string]string{"If-Match": etag}, 204)
		request(t, "VIEWER", "GET", public, "", nil, 404)
		g = request(t, "SUPER_ADMIN", "GET", grantPath, "", nil, 200)
		revoked := g.Header().Get("ETag")
		require.Equal(t, false, data(g)["is_active"])
		require.NotEqual(t, etag, revoked)
		before = snapshot()
		request(t, "SUPER_ADMIN", "DELETE", grantPath, "", map[string]string{"If-Match": revoked}, 204)
		require.Equal(t, before, snapshot())
		request(t, "SUPER_ADMIN", "PUT", grantPath, `{"is_active":true}`, map[string]string{"If-None-Match": "*"}, 412)
		request(t, "SUPER_ADMIN", "PUT", grantPath, `{"is_active":true}`, map[string]string{"If-Match": etag}, 412)
		request(t, "SUPER_ADMIN", "PUT", grantPath, `{"is_active":true}`, map[string]string{"If-Match": revoked}, 200)
		request(t, "VIEWER", "GET", public, "", nil, 200)
		v = data(request(t, "SUPER_ADMIN", "GET", admin+"/staff-grants?limit=2", "", nil, 200))
		require.Equal(t, float64(4), v["total"])
		require.Len(t, v["items"], 2)
		newPath := "/api/v1/admin/apps/" + b.PublicID.String() + "/staff-grants/" + target
		w = request(t, "SUPER_ADMIN", "PUT", newPath, `{"is_active":true}`, map[string]string{"If-None-Match": "*"}, 201)
		require.Equal(t, newPath, w.Header().Get("Location"))
		require.Equal(t, "1", data(w)["revision"])
	})
	t.Run("strict invalid requests leave state untouched", func(t *testing.T) {
		before := snapshot()
		for _, body := range []string{`{}`, `null`, `[]`, `{"name":null}`, `{"Name":"x"}`, `{"name":"x","name":"y"}`, `{"name":"x","na\u006de":"y"}`, `{"name":"x","status":"ACTIVE"}`, `{"name":"x","id":"protected"}`, `{"name":"x","credentials":{}}`, `{"name":"x"} {}`, `{"name":"bad\nname"}`, "{\"name\":\"\xff\"}"} {
			request(t, "SUPER_ADMIN", "PATCH", admin, body, map[string]string{"If-Match": `"tag"`}, 400)
		}
		for _, body := range []string{`{}`, `{"is_active":null}`, `{"is_active":false}`, `{"Is_active":true}`, `{"is_active":true,"staff_id":"1"}`} {
			request(t, "SUPER_ADMIN", "PUT", grantPath, body, map[string]string{"If-None-Match": "*"}, 400)
		}
		for _, headers := range []map[string]string{{"Content-Type": ""}, {"Content-Type": "text/plain"}, {"Content-Type": "application/json; charset=ascii"}, {"Content-Encoding": "gzip"}} {
			request(t, "SUPER_ADMIN", "PATCH", admin, `{"name":"x"}`, headers, 415)
		}
		request(t, "SUPER_ADMIN", "PATCH", admin, `{"name":"x"}`, nil, 428)
		request(t, "SUPER_ADMIN", "PUT", grantPath, `{"is_active":true}`, nil, 428)
		request(t, "SUPER_ADMIN", "DELETE", grantPath, "", nil, 428)
		for _, headers := range []map[string]string{{"If-Match": ""}, {"If-Match": `W/"tag"`}, {"If-Match": "*"}, {"If-Match": `"a", "b"`}, {"If-Match": `"a"`, "If-None-Match": "*"}, {"If-None-Match": "*"}} {
			request(t, "SUPER_ADMIN", "PATCH", admin, `{"name":"x"}`, headers, 400)
		}
		request(t, "SUPER_ADMIN", "POST", "/api/v1/admin/apps", `{"key":"m03-http-created","name":"x"}`, map[string]string{"If-None-Match": "*"}, 400)
		request(t, "SUPER_ADMIN", "DELETE", grantPath, " ", map[string]string{"If-Match": `"tag"`}, 400)
		request(t, "SUPER_ADMIN", "DELETE", grantPath, strings.Repeat(" ", 65537), nil, 413)
		request(t, "SUPER_ADMIN", "PATCH", admin, `{"name":"x"}`+strings.Repeat(" ", 65536), nil, 413)
		for _, path := range []string{"/api/v1/admin/apps/not-a-uuid", admin + "/staff-grants/0", admin + "/staff-grants/01", admin + "/staff-grants/9223372036854775808", admin + "?unknown=1", "/api/v1/apps?key=m03-http-a", "/api/v1/apps?page=1&page=2", "/api/v1/apps?page=0", "/api/v1/apps?limit=101", "/api/v1/apps?%zz=1"} {
			request(t, "SUPER_ADMIN", "GET", path, "", nil, 400)
		}
		missingPath := admin + "/staff-grants/9223372036854775807"
		request(t, "SUPER_ADMIN", "GET", missingPath, "", nil, 404)
		request(t, "SUPER_ADMIN", "PUT", missingPath, `{"is_active":true}`, map[string]string{"If-None-Match": "*"}, 404)
		require.Equal(t, before, snapshot())
	})
	t.Run("current authority overrides existing JWT", func(t *testing.T) {
		_, err = pool.Exec(ctx, "UPDATE staff_users SET is_active=false WHERE id=$1", actors["VIEWER"])
		require.NoError(t, err)
		request(t, "VIEWER", "GET", "/api/v1/apps", "", nil, 401)
		current := request(t, "SUPER_ADMIN", "GET", grantPath, "", nil, 200).Header().Get("ETag")
		request(t, "SUPER_ADMIN", "PUT", grantPath, `{"is_active":true}`, map[string]string{"If-Match": current}, 409)
		_, err = pool.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1", actors["SUPER_ADMIN"])
		require.NoError(t, err)
		request(t, "SUPER_ADMIN", "GET", "/api/v1/admin/apps", "", nil, 403)
		request(t, "SUPER_ADMIN", "GET", public, "", nil, 200)
	})
	t.Run("database failure stays private", func(t *testing.T) {
		// Closed dedicated pool forces the real JWT active lookup to fail.
		broken, e := database.New(ctx, databaseURL)
		require.NoError(t, e)
		broken.Close()
		bm := http.NewServeMux()
		bq := sqlc.New(broken)
		applications.NewModule(bq, broken, middleware.NewAPIKeyMiddleware("registry-fixture-key").Use, middleware.NewJWTMiddleware(manager, repository.NewStaffUserRepository(bq)).Use).RegisterRoutes(bm)
		r := httptest.NewRequest("GET", "/api/v1/apps", nil)
		r.Header.Set("X-API-Key", "registry-fixture-key")
		r.Header.Set("Authorization", "Bearer "+tokens["ADMIN"])
		w := httptest.NewRecorder()
		bm.ServeHTTP(w, r)
		require.Equal(t, 500, w.Code)
		require.JSONEq(t, `{"success":false,"message":"internal server error"}`, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	})
}
