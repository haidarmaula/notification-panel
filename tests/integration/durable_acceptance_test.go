//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/applications"
	"hello/internal/installation"
	"hello/internal/middleware"
	"hello/internal/token"
)

// A separate schema permits real committed concurrent transactions without
// relaxing project status constraints in other integration fixtures.
func acceptanceFixture(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	base, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	name := fmt.Sprintf("acceptance_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{name}.Sanitize()
	_, err = base.Exec(ctx, "CREATE SCHEMA "+identifier)
	require.NoError(t, err)
	cfg, err := pgxpool.ParseConfig(databaseURL)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = name + ",pg_catalog"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := base.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE")
		require.NoError(t, err)
		base.Close()
	})
	files, err := filepath.Glob("../../db/migrations/*.up.sql")
	require.NoError(t, err)
	sort.Strings(files)
	for _, file := range files {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		// Rebind schema-qualified privileged routines to this disposable schema.
		data = bytes.ReplaceAll(data, []byte("public."), []byte(identifier+"."))
		_, err = pool.Exec(ctx, string(data))
		require.NoError(t, err, file)
	}
	return pool
}

func TestDurableAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := acceptanceFixture(t, ctx)
	q := sqlc.New(pool)
	service := applications.NewService(q, pool)
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, query, args...)
		require.NoError(t, err)
	}
	count := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		require.NoError(t, pool.QueryRow(ctx, query, args...).Scan(&n))
		return n
	}
	exec("ALTER TABLE applications DROP CONSTRAINT applications_status_check")
	var appID int64
	var project pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO applications(key,name,status) VALUES('acceptance-app','App','ACTIVE') RETURNING id,public_id").Scan(&appID, &project))
	actors := map[string]int64{}
	tokens := map[string]string{}
	manager := token.NewTokenManager("fixture-access", "fixture-refresh")
	for _, role := range []string{"VIEWER", "OPERATOR", "ADMIN", "SUPER_ADMIN"} {
		id := count("INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Acceptance fixture',$1,'fixture' FROM roles WHERE name=$2 RETURNING id", role+"@fixture.invalid", role)
		actors[role] = id
		exec("INSERT INTO application_staff_grants(application_id,staff_id) VALUES($1,$2)", appID, id)
		var err error
		tokens[role], err = fixtureAccess(ctx, q, manager, id, "VIEWER", "")
		require.NoError(t, err)
	}
	actorCtx := context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: actors["ADMIN"]})
	userID := count("INSERT INTO users(application_id,external_id) VALUES($1,'acceptance-user') RETURNING id", appID)
	protector, err := installation.NewProtector(1, map[int32][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	lifecycle, err := installation.NewLifecycle(pool, protector)
	require.NoError(t, err)
	_, err = lifecycle.Enroll(ctx, installation.Principal{ApplicationID: appID, UserID: userID}, installation.Route{Namespace: "fixture-project", Key: "fixture-route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}, "fixture-address")
	require.NoError(t, err)
	approveFixtureBindings(t, ctx, pool, appID)
	newDraft := func() int64 {
		id := count("INSERT INTO notifications(application_id,title,body,created_by) VALUES($1,'Acceptance','Body',$2) RETURNING id", appID, actors["ADMIN"])
		exec("INSERT INTO notification_targets(application_id,notification_id,target_type) VALUES($1,$2,'BROADCAST')", appID, id)
		return id
	}
	mux := http.NewServeMux()
	applications.NewModule(q, pool, middleware.NewAPIKeyMiddleware("fixture-key").Use, middleware.NewJWTMiddleware(manager, repository.NewStaffUserRepository(q)).Use, middleware.NewAuditMiddleware().Use).RegisterRoutes(mux)
	request := func(role string, id int64, operation, key, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/apps/"+project.String()+"/campaigns/"+strconv.FormatInt(id, 10)+"/"+operation, bytes.NewBufferString(body)).WithContext(ctx)
		r.Header.Set("X-API-Key", "fixture-key")
		if role != "" {
			r.Header.Set("Authorization", "Bearer "+tokens[role])
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		require.Equal(t, want, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		if want == 202 {
			example := rolloutExample(t, "post", "/campaigns/{campaign_id}/"+operation, "202")
			var expected, actual struct {
				Success bool              `json:"success"`
				Message string            `json:"message"`
				Data    map[string]string `json:"data"`
			}
			require.NoError(t, json.Unmarshal(example, &expected))
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &actual))
			require.Equal(t, expected.Success, actual.Success)
			require.Equal(t, expected.Message, actual.Message)
			require.Equal(t, expected.Data["status"], actual.Data["status"])
			require.Equal(t, strconv.FormatInt(id, 10), actual.Data["id"])
			require.Len(t, actual.Data, len(expected.Data))
			for field := range expected.Data {
				require.Contains(t, actual.Data, field)
				if strings.HasSuffix(field, "_at") {
					_, err := time.Parse(time.RFC3339Nano, actual.Data[field])
					require.NoError(t, err)
				}
			}
		}
		return w
	}
	draft := newDraft()
	request("", draft, "send", "", "", 401)
	for _, role := range []string{"VIEWER", "OPERATOR"} {
		request(role, draft, "send", "", "", 403)
		request(role, draft, "schedule", "", `{"scheduled_at":"2099-01-01T00:00:00Z"}`, 403)
	}
	request("ADMIN", draft, "send", "first", "", 503)
	require.Zero(t, count("SELECT count(*) FROM notification_send_outbox"))
	exec("INSERT INTO application_dispatch_policy(application_id,hold_new,coverage_approved,push_ttl_seconds,key_ttl_seconds) VALUES($1,false,true,3600,86400)", appID)
	exec("UPDATE notification_runtime SET sealed=false,migration_hold=false")

	// Concurrent commits must produce one acceptance and one stored replay.
	var wg sync.WaitGroup
	type outcome struct {
		result applications.Acceptance
		replay bool
		err    error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, replay, err := service.AcceptCampaign(actorCtx, project, draft, "concurrent-key")
			results <- outcome{result, replay, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	replays := 0
	var accepted applications.Acceptance
	for result := range results {
		require.NoError(t, result.err)
		if result.replay {
			replays++
		}
		if accepted.ID != "" {
			require.Equal(t, accepted, result.result)
		}
		accepted = result.result
	}
	require.Equal(t, 1, replays)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notification_send_keys"))
	require.EqualValues(t, 1, count("SELECT count(*) FROM notification_send_outbox"))
	require.EqualValues(t, 1, count("SELECT count(*) FROM audit_logs WHERE action='NOTIFICATION_SEND'"))
	exec("UPDATE notification_runtime SET migration_hold=true")
	w := request("ADMIN", draft, "send", "concurrent-key", "", 202)
	require.Equal(t, "true", w.Header().Get("Idempotency-Replayed"))
	other := newDraft()
	request("ADMIN", other, "send", "concurrent-key", "", 409)
	request("ADMIN", other, "send", "new-key", "", 503)
	exec("UPDATE application_staff_grants SET is_active=false WHERE application_id=$1 AND staff_id=$2", appID, actors["ADMIN"])
	request("ADMIN", draft, "send", "concurrent-key", "", 404)
	exec("UPDATE application_staff_grants SET is_active=true WHERE application_id=$1 AND staff_id=$2", appID, actors["ADMIN"])
	exec("UPDATE notification_runtime SET migration_hold=false")
	legacy := count("INSERT INTO device_tokens(user_id,provider,platform,push_token) VALUES($1,'FCM','ANDROID','legacy-fixture') RETURNING id", userID)
	request("ADMIN", other, "send", "coverage", "", 409)
	exec("UPDATE device_tokens SET is_active=false WHERE id=$1", legacy)
	exec("ALTER TABLE audit_logs ADD CONSTRAINT reject_send_audit CHECK(action <> 'NOTIFICATION_SEND') NOT VALID")
	request("ADMIN", other, "send", "rollback", "", 500)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notifications WHERE id=$1 AND status='DRAFT' AND queued_at IS NULL", other))
	require.EqualValues(t, 1, count("SELECT count(*) FROM notification_send_keys"))
	require.EqualValues(t, 1, count("SELECT count(*) FROM notification_send_outbox"))
	exec("ALTER TABLE audit_logs DROP CONSTRAINT reject_send_audit")
	w = request("SUPER_ADMIN", other, "send", "", "", 202)
	require.Equal(t, "false", w.Header().Get("Idempotency-Replayed"))

	scheduled := newDraft()
	at := time.Now().Add(time.Minute).UTC()
	body := fmt.Sprintf(`{"scheduled_at":%q}`, at.Format(time.RFC3339Nano))
	request("ADMIN", scheduled, "schedule", "", body, 202)
	require.EqualValues(t, 0, count("SELECT count(*) FROM notification_send_outbox WHERE notification_id=$1", scheduled))
	changed, err := service.PromoteSchedule(ctx, project, scheduled)
	require.NoError(t, err)
	require.False(t, changed)
	exec("UPDATE notifications SET scheduled_at=clock_timestamp()-interval '1 second' WHERE id=$1", scheduled)
	exec("UPDATE notification_runtime SET migration_hold=true")
	changed, err = service.PromoteSchedule(ctx, project, scheduled)
	require.NoError(t, err)
	require.True(t, changed)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notification_send_outbox WHERE notification_id=$1 AND scheduled_for IS NOT NULL", scheduled))
	changed, err = service.PromoteSchedule(ctx, project, scheduled)
	require.NoError(t, err)
	require.False(t, changed)
	exec("UPDATE notification_runtime SET migration_hold=false")
	expired := newDraft()
	request("ADMIN", expired, "schedule", "", body, 202)
	exec("UPDATE notifications SET scheduled_at=clock_timestamp()-interval '2 hours',deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", expired)
	changed, err = service.PromoteSchedule(ctx, project, expired)
	require.NoError(t, err)
	require.True(t, changed)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notifications WHERE id=$1 AND status='FAILED' AND lifecycle_reason='ttl_expired'", expired))
	require.Zero(t, count("SELECT count(*) FROM notification_send_outbox WHERE notification_id=$1", expired))

	// Opting into a versioned retention policy transfers deletion authority to
	// the batch workflow, including the interval while its first hold is active.
	nextDraft := newDraft()
	exec("UPDATE notification_send_keys SET queued_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 minute' WHERE key='concurrent-key'")
	exec("INSERT INTO retention_policies(application_id,record_class,policy_version,retain_after_expiry_seconds) VALUES($1,'send_key',gen_random_uuid(),60)", appID)
	request("ADMIN", nextDraft, "send", "concurrent-key", "", 409)
	exec("UPDATE retention_policies SET held=false WHERE application_id=$1 AND record_class='send_key'", appID)
	request("ADMIN", nextDraft, "send", "concurrent-key", "", 409)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notification_send_keys WHERE notification_id=$1 AND key='concurrent-key'", draft))
	require.EqualValues(t, 1, count("SELECT count(*) FROM notifications WHERE id=$1 AND status='DRAFT'", nextDraft))

	// A project without any send-key maintenance policy keeps M04's previous
	// authenticated reuse semantics. Even then an accepted campaign cannot resend.
	exec("DELETE FROM retention_policies WHERE application_id=$1 AND record_class='send_key'", appID)
	request("ADMIN", nextDraft, "send", "concurrent-key", "", 202)
	request("ADMIN", draft, "send", "different-key", "", 409)
}
