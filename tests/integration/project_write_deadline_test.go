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
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/applications"
	"hello/internal/middleware"
	"hello/internal/token"
)

func TestProjectWriteBudgetCancelsLocks(t *testing.T) {
	setup, cancelSetup := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancelSetup()
	pool := acceptanceFixture(t, setup)
	q := sqlc.New(pool)
	_, err := pool.Exec(setup, "ALTER TABLE applications DROP CONSTRAINT applications_status_check")
	require.NoError(t, err)
	var app sqlc.Application
	require.NoError(t, pool.QueryRow(setup, "INSERT INTO applications(key,name,status) VALUES('write-budget','Write budget','ACTIVE') RETURNING id,public_id").
		Scan(&app.ID, &app.PublicID))
	app, err = q.GetApplicationByPublicID(setup, app.PublicID)
	require.NoError(t, err)
	var staff int64
	require.NoError(t, pool.QueryRow(setup, "INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Budget fixture','budget@fixture.invalid','fixture-only' FROM roles WHERE name='SUPER_ADMIN' RETURNING id").Scan(&staff))
	_, err = pool.Exec(setup, "INSERT INTO application_staff_grants(application_id,staff_id) VALUES($1,$2)", app.ID, staff)
	require.NoError(t, err)
	manager := token.NewTokenManager("fixture-access", "fixture-refresh")
	bearer, err := fixtureAccess(setup, q, manager, staff, "SUPER_ADMIN", "")
	require.NoError(t, err)
	mux := http.NewServeMux()
	applications.NewModule(q, pool, middleware.NewAPIKeyMiddleware("fixture-key").Use,
		middleware.NewJWTMiddleware(manager, repository.NewStaffUserRepository(q)).Use,
		middleware.NewAuditMiddleware().Use).RegisterRoutes(mux)

	cases := []struct {
		name, method, path, body string
		earlier                  bool
		success                  int
	}{
		{"registry default deadline", http.MethodPatch, "/api/v1/admin/apps/" + app.PublicID.String(), `{"name":"Renamed"}`, false, http.StatusOK},
		{"project earlier deadline", http.MethodPost, "/api/v1/apps/" + app.PublicID.String() + "/segments", `{"name":"Bounded"}`, true, http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := func(ctx context.Context) *httptest.ResponseRecorder {
				r := httptest.NewRequestWithContext(ctx, tc.method, tc.path, strings.NewReader(tc.body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Authorization", "Bearer "+bearer)
				r.Header.Set("X-API-Key", "fixture-key")
				if tc.method == http.MethodPatch {
					r.Header.Set("If-Match", applications.ApplicationETag(app))
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				return w
			}
			snapshot := func() string {
				t.Helper()
				var state string
				require.NoError(t, pool.QueryRow(setup, `SELECT jsonb_build_array(
					(SELECT to_jsonb(a) FROM applications a WHERE id=$1),
					(SELECT count(*) FROM segments),(SELECT count(*) FROM audit_logs))::text`, app.ID).Scan(&state))
				return state
			}
			before := snapshot()
			blocker, err := pool.Begin(setup)
			require.NoError(t, err)
			defer blocker.Rollback(context.Background())
			_, err = blocker.Exec(setup, "SELECT id FROM applications WHERE id=$1 FOR UPDATE", app.ID)
			require.NoError(t, err)
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.earlier {
				var stop context.CancelFunc
				caller, stop = context.WithTimeout(caller, time.Second)
				defer stop()
			}
			result := make(chan *httptest.ResponseRecorder, 1)
			started := time.Now()
			go func() { result <- call(caller) }()
			require.Eventually(t, func() bool {
				var waiting bool
				err := pool.QueryRow(setup, "SELECT EXISTS(SELECT FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))",
					int32(blocker.Conn().PgConn().PID())).Scan(&waiting)
				return err == nil && waiting
			}, time.Second, 10*time.Millisecond)
			select {
			case response := <-result:
				require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
				require.JSONEq(t, `{"success":false,"message":"internal server error"}`, response.Body.String())
			case <-time.After(8 * time.Second):
				cancel()
				<-result
				t.Fatal("write exceeded its operation budget")
			}
			if tc.earlier {
				require.ErrorIs(t, caller.Err(), context.DeadlineExceeded)
				require.Less(t, time.Since(started), 3*time.Second)
			} else {
				require.NoError(t, caller.Err(), "the internal deadline must leave the caller live")
				require.Less(t, time.Since(started), 8*time.Second)
			}
			require.Eventually(t, func() bool { return pool.Stat().AcquiredConns() == 1 }, time.Second, 10*time.Millisecond)
			require.Equal(t, before, snapshot(), "timed-out writes must leave resource and audit unchanged")
			require.NoError(t, blocker.Rollback(setup))
			response := call(setup)
			require.Equal(t, tc.success, response.Code, response.Body.String())
			require.NotEqual(t, before, snapshot(), "the released pool and resource remain usable")
		})
	}
}
