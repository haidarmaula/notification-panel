//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"hello/internal/database"
)

func TestBusinessApplicationScopeMigration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	read := func(path string) string {
		t.Helper()
		b, e := os.ReadFile("../../" + path)
		require.NoError(t, e)
		return string(b)
	}
	up := read("db/migrations/000017_expand_business_application_scope.up.sql")
	down := read("db/migrations/000017_expand_business_application_scope.down.sql")
	backfill := read("scripts/sql/backfill-reviewed-single-application.sql")
	preflight := read("scripts/sql/application-scope-preflight.sql")
	// The script refuses an ordinary autocommit transaction before touching data.
	_, err = pool.Exec(ctx, backfill)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23514", pgErr.Code)
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	exec := func(sql string, args ...any) { t.Helper(); _, e := tx.Exec(ctx, sql, args...); require.NoError(t, e) }
	id := func(sql string, args ...any) int64 {
		t.Helper()
		var n int64
		require.NoError(t, tx.QueryRow(ctx, sql, args...).Scan(&n))
		return n
	}
	expect := func(code, sql string, args ...any) {
		t.Helper()
		exec("SAVEPOINT scope_case")
		_, e := tx.Exec(ctx, sql, args...)
		var pe *pgconn.PgError
		require.ErrorAs(t, e, &pe)
		require.Equal(t, code, pe.Code)
		exec("ROLLBACK TO SAVEPOINT scope_case")
		exec("RELEASE SAVEPOINT scope_case")
	}
	tables := []string{"users", "device_tokens", "templates", "segments", "segment_members", "notifications", "upload_batches", "upload_batch_rows", "notification_targets", "notification_deliveries", "notification_reads"}
	snapshot := func(withOwner bool) string {
		t.Helper()
		v := map[string]json.RawMessage{}
		for _, table := range append(append([]string{}, tables...), "audit_logs") {
			expr := "to_jsonb(r)"
			if !withOwner {
				expr += "-'application_id'"
			}
			var b []byte
			require.NoError(t, tx.QueryRow(ctx, "SELECT coalesce(jsonb_agg("+expr+" ORDER BY id),'[]'::jsonb) FROM "+table+" r").Scan(&b))
			v[table] = b
		}
		b, e := json.Marshal(v)
		require.NoError(t, e)
		return string(b)
	}
	exec(read("db/migrations/000021_create_mobile_origin_uses.down.sql"))
	exec(read("db/migrations/000020_create_mobile_identities.down.sql"))
	exec(read("db/migrations/000018_create_installation_bindings.down.sql"))
	exec(down)
	staff := id(`INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Scope fixture',gen_random_uuid()::text || '@fixture.invalid','fixture-only' FROM roles WHERE name='SUPER_ADMIN' RETURNING id`)
	a := id(`INSERT INTO applications(key,name) VALUES('scope-migrate-a','Scope A') RETURNING id`)
	b := id(`INSERT INTO applications(key,name) VALUES('scope-migrate-b','Scope B') RETURNING id`)
	var public string
	require.NoError(t, tx.QueryRow(ctx, "SELECT public_id::text FROM applications WHERE id=$1", a).Scan(&public))
	user := id(`INSERT INTO users(external_id) VALUES('scope-legacy-subject') RETURNING id`)
	device := id(`INSERT INTO device_tokens(user_id,provider,platform,push_token) VALUES($1,'FCM','ANDROID','scope-legacy-token') RETURNING id`, user)
	template := id(`INSERT INTO templates(name,title_template,body_template,created_by) VALUES('Scope template','Title','Body',$1) RETURNING id`, staff)
	segment := id(`INSERT INTO segments(name,created_by) VALUES('Scope segment',$1) RETURNING id`, staff)
	exec(`INSERT INTO segment_members(segment_id,user_id) VALUES($1,$2)`, segment, user)
	notification := id(`INSERT INTO notifications(template_id,title,body,created_by) VALUES($1,'Scope title','Body',$2) RETURNING id`, template, staff)
	batch := id(`INSERT INTO upload_batches(uploaded_by,original_filename) VALUES($1,'fixture.csv') RETURNING id`, staff)
	exec(`INSERT INTO upload_batch_rows(batch_id,external_id) VALUES($1,'scope-legacy-subject')`, batch)
	exec(`INSERT INTO notification_targets(notification_id,target_type,segment_id,user_id,upload_batch_id) VALUES($1,'SEGMENT',$2,$3,$4)`, notification, segment, user, batch)
	delivery := id(`INSERT INTO notification_deliveries(notification_id,user_id,device_token_id,provider) VALUES($1,$2,$3,'fcm') RETURNING id`, notification, user, device)
	exec(`INSERT INTO notification_reads(notification_id,user_id) VALUES($1,$2)`, notification, user)
	exec(`INSERT INTO audit_logs(actor_user_id,action,entity_type,entity_id,before_json) VALUES($1,'FIXTURE','notification',$2,'{"safe":"fixture"}')`, staff, notification)
	exec(`INSERT INTO audit_logs(actor_user_id,action,entity_type,entity_id) VALUES($1,'FIXTURE','segment',9223372036854775807),($1,'FIXTURE','staff_user',$1)`, staff)
	before := snapshot(false)
	exec(up)
	require.JSONEq(t, before, snapshot(false))
	// Nullable columns have no implicit application/default, and rollback preserves legacy data.
	exec(down)
	require.JSONEq(t, before, snapshot(false))
	exec(up)
	var report struct {
		Ownership           map[string]struct{ Unassigned, Assigned int64 }
		DeviceUserConflicts int64 `json:"device_user_conflicts"`
		Unresolved          int64 `json:"unresolved_business_audits"`
	}
	var raw []byte
	require.NoError(t, tx.QueryRow(ctx, preflight).Scan(&raw))
	require.NoError(t, json.Unmarshal(raw, &report))
	require.Len(t, report.Ownership, 11)
	for _, counts := range report.Ownership {
		require.Equal(t, int64(1), counts.Unassigned)
		require.Zero(t, counts.Assigned)
	}
	require.Equal(t, int64(1), report.Unresolved)
	var nullable, defaults int64
	require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE is_nullable='YES'),count(*) FILTER(WHERE column_default IS NOT NULL) FROM information_schema.columns WHERE table_schema='public' AND table_name=ANY($1) AND column_name='application_id'`, tables).Scan(&nullable, &defaults))
	require.Equal(t, int64(11), nullable)
	require.Zero(t, defaults)
	expect("23514", backfill)
	exec(`SELECT set_config('hello.scope_mapping_ack','one-app-ownership-reviewed',true)`)
	expect("23514", backfill) // explicit UUID required, even with two registry entries
	exec(`SELECT set_config('hello.scope_target_public_id','00000000-0000-0000-0000-000000000001',true)`)
	expect("23514", backfill)
	exec(`SELECT set_config('hello.scope_target_public_id',$1,true)`, public)
	foreign := id(`INSERT INTO users(application_id,external_id) VALUES($1,'foreign-owned') RETURNING id`, b)
	expect("23514", backfill)
	exec("DELETE FROM users WHERE id=$1", foreign)
	other := id(`INSERT INTO users(external_id) VALUES('scope-other-user') RETURNING id`)
	exec("UPDATE notification_deliveries SET user_id=$1 WHERE id=$2", other, delivery)
	require.NoError(t, tx.QueryRow(ctx, preflight).Scan(&raw))
	require.NoError(t, json.Unmarshal(raw, &report))
	require.Equal(t, int64(1), report.DeviceUserConflicts)
	expect("23514", backfill)
	exec("UPDATE notification_deliveries SET user_id=$1 WHERE id=$2", user, delivery)
	exec("DELETE FROM users WHERE id=$1", other)
	// Failure at the last business UPDATE must roll back every earlier assignment.
	exec("ALTER TABLE notification_reads ADD CONSTRAINT fixture_scope_rejection CHECK(application_id IS NULL)")
	beforeFailure := snapshot(true)
	expect("23514", backfill)
	require.JSONEq(t, beforeFailure, snapshot(true))
	exec("ALTER TABLE notification_reads DROP CONSTRAINT fixture_scope_rejection")
	exec(backfill)
	require.JSONEq(t, before, snapshot(false))
	for _, table := range tables {
		var n int64
		require.NoError(t, tx.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE application_id=$1", a).Scan(&n))
		require.Equal(t, int64(1), n, table)
	}
	// Migration does not edit business content/timestamps, and maps only resolvable audit scope.
	var mapped, unresolved, global int64
	require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE application_id=$1),count(*) FILTER(WHERE application_id IS NULL AND entity_type='segment'),count(*) FILTER(WHERE application_id IS NULL AND entity_type='staff_user') FROM audit_logs WHERE actor_user_id=$2`, a, staff).Scan(&mapped, &unresolved, &global))
	require.Equal(t, int64(1), mapped)
	require.Equal(t, int64(1), unresolved)
	require.Equal(t, int64(1), global)
	stable := snapshot(true)
	exec(backfill)
	require.JSONEq(t, stable, snapshot(true))
	expect("23514", down) // refuse destructive rollback after assignment
	for _, table := range tables {
		expect("23514", "UPDATE "+table+" SET application_id=$1", b)
		expect("23514", "UPDATE "+table+" SET application_id=NULL")
		exec("UPDATE " + table + " SET application_id=application_id")
	}
	// Cross-app composite references reject new rows, not just owner UPDATEs.
	foreignUser := id(`INSERT INTO users(application_id,external_id) VALUES($1,'foreign-scope-subject') RETURNING id`, b)
	expect("23503", `INSERT INTO device_tokens(application_id,user_id,provider,platform,push_token) VALUES($1,$2,'FCM','IOS','cross-device')`, b, user)
	expect("23503", `INSERT INTO segment_members(application_id,segment_id,user_id) VALUES($1,$2,$3)`, a, segment, foreignUser)
	expect("23503", `INSERT INTO notifications(application_id,template_id,title,body,created_by) VALUES($1,$2,'cross','body',$3)`, b, template, staff)
	expect("23503", `INSERT INTO upload_batch_rows(application_id,batch_id,external_id) VALUES($1,$2,'cross')`, b, batch)
	expect("23503", `INSERT INTO notification_targets(application_id,notification_id,target_type) VALUES($1,$2,'BROADCAST')`, b, notification)
	expect("23503", `INSERT INTO notification_deliveries(application_id,notification_id,user_id,device_token_id,provider) VALUES($1,$2,$3,$4,'fcm')`, b, notification, user, device)
	expect("23503", `INSERT INTO notification_reads(application_id,notification_id,user_id) VALUES($1,$2,$3)`, a, notification, foreignUser)
	other = id(`INSERT INTO users(application_id,external_id) VALUES($1,'other-same-app') RETURNING id`, a)
	expect("23503", `INSERT INTO notification_deliveries(application_id,notification_id,user_id,device_token_id,provider) VALUES($1,$2,$3,$4,'fcm')`, a, notification, other, device)
	// Extra nonnull target edges are scoped even though the old target CHECK permits them.
	otherSegment := id(`INSERT INTO segments(application_id,name,created_by) VALUES($1,'Scope B segment',$2) RETURNING id`, b, staff)
	otherNotification := id(`INSERT INTO notifications(application_id,title,body,created_by) VALUES($1,'B','body',$2) RETURNING id`, b, staff)
	expect("23503", `INSERT INTO notification_targets(application_id,notification_id,target_type,segment_id,user_id) VALUES($1,$2,'SEGMENT',$3,$4)`, b, otherNotification, otherSegment, user)
	// Template SET NULL must preserve the notification's established app owner.
	exec("DELETE FROM templates WHERE id=$1", template)
	var owner int64
	var missing bool
	require.NoError(t, tx.QueryRow(ctx, "SELECT application_id,template_id IS NULL FROM notifications WHERE id=$1", notification).Scan(&owner, &missing))
	require.Equal(t, a, owner)
	require.True(t, missing)
	// Existing subject/address uniqueness deliberately remains globally conservative.
	expect("23505", `INSERT INTO users(application_id,external_id) VALUES($1,'scope-legacy-subject')`, b)
	expect("23505", `INSERT INTO device_tokens(application_id,user_id,provider,platform,push_token) VALUES($1,$2,'FCM','ANDROID','scope-legacy-token')`, a, user)
	// Existing cascades still work with the additional composite constraints.
	exec("DELETE FROM users WHERE id=$1", user)
	require.Zero(t, id("SELECT count(*) FROM device_tokens WHERE id=$1", device))
	require.Zero(t, id("SELECT count(*) FROM notification_deliveries WHERE id=$1", delivery))
	finalize, err := os.ReadFile("../../scripts/sql/finalize-application-ownership.sql")
	require.NoError(t, err)
	unassigned := id("INSERT INTO upload_batches(uploaded_by,original_filename) VALUES($1,'unassigned') RETURNING id", staff)
	expect("23502", string(finalize))
	// The rejected statement must roll back NOT NULL on earlier tables.
	probe := id("INSERT INTO users(external_id) VALUES('scope-finalization-probe') RETURNING id")
	exec("DELETE FROM users WHERE id=$1", probe)
	exec("DELETE FROM upload_batches WHERE id=$1", unassigned)
	exec(string(finalize))
	require.EqualValues(t, 11, id(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema='public' AND column_name='application_id' AND is_nullable='NO'
		AND table_name IN ('users','device_tokens','templates','segments','segment_members','notifications',
			'upload_batches','upload_batch_rows','notification_targets','notification_deliveries','notification_reads')`))
	expect("23502", "INSERT INTO users(external_id) VALUES('rejected-unassigned')")

}
