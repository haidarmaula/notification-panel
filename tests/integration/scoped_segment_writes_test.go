//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"hello/internal/database"
	"hello/internal/database/sqlc"
	"hello/internal/features/applications"
	"hello/internal/middleware"
	"hello/internal/token"
)

// Each service transaction is a savepoint inside an isolated fixture transaction.
type segmentWriteFixture struct{ pgx.Tx }

func (f segmentWriteFixture) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	return f.Begin(ctx)
}

func TestScopedSegmentWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := tx.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	number := func(sql string, args ...any) int64 {
		t.Helper()
		var id int64
		require.NoError(t, tx.QueryRow(ctx, sql, args...).Scan(&id))
		return id
	}
	staff := number("INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Write fixture','scope-writes@example.invalid','fixture' FROM roles WHERE name='SUPER_ADMIN' RETURNING id")
	var appID int64
	var public string
	require.NoError(t, tx.QueryRow(ctx, "INSERT INTO applications(key,name) VALUES('scope-writes','Writes') RETURNING id,public_id::text").Scan(&appID, &public))
	other := number("INSERT INTO applications(key,name) VALUES('scope-other','Other') RETURNING id")
	user := number("INSERT INTO users(application_id,external_id) VALUES($1,'scoped-write-user') RETURNING id", appID)
	foreign := number("INSERT INTO users(application_id,external_id) VALUES($1,'scoped-write-foreign') RETURNING id", other)
	exec("INSERT INTO application_staff_grants(application_id,staff_id) VALUES($1,$2)", appID, staff)
	mux := http.NewServeMux()
	applications.NewModule(sqlc.New(tx), segmentWriteFixture{tx}).RegisterRoutes(mux)
	base := "/api/v1/apps/" + public + "/segments"
	request := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: staff}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		require.Equal(t, status, w.Code, w.Body.String())
		return w
	}
	request(http.MethodPost, base, `{"name":"Audience"}`, 409)
	// The production schema only accepts PROVISIONING. Exercise the future
	// ACTIVE path inside this rollback-only transaction.
	exec("ALTER TABLE applications DROP CONSTRAINT applications_status_check")
	exec("UPDATE applications SET status='ACTIVE' WHERE id=$1", appID)
	exec("ALTER TABLE audit_logs ADD CONSTRAINT reject_fixture_create CHECK(action <> 'SEGMENT_CREATE') NOT VALID")
	request(http.MethodPost, base, `{"name":"Audience"}`, 500)
	require.Zero(t, number("SELECT count(*) FROM segments WHERE application_id=$1", appID))
	exec("ALTER TABLE audit_logs DROP CONSTRAINT reject_fixture_create")

	w := request(http.MethodPost, base, `{"name":"Audience","description":"test"}`, 201)
	var envelope struct {
		Data struct {
			ID string `json:"id"`
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	path := base + "/" + envelope.Data.ID
	request(http.MethodPost, base, `{"name":"Audience"}`, 409)
	request(http.MethodPut, path, `{"name":"Renamed"}`, 200)
	request(http.MethodPut, fmt.Sprintf("%s/members/%d", path, foreign), "", 404)
	memberPath := fmt.Sprintf("%s/members/%d", path, user)
	request(http.MethodPut, memberPath, "", 204)
	request(http.MethodPut, memberPath, "", 409)
	request(http.MethodDelete, path, "", 409)
	request(http.MethodDelete, memberPath, "", 204)
	request(http.MethodDelete, memberPath, "", 404)
	request(http.MethodDelete, path, "", 204)
	require.Zero(t, number("SELECT count(*) FROM segments WHERE application_id=$1", appID))
	require.EqualValues(t, 5, number("SELECT count(*) FROM audit_logs WHERE application_id=$1 AND actor_user_id=$2", appID, staff))
}
