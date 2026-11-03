//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/database"
)

func TestHistoricalStaffAuditRedaction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	var actor int64
	require.NoError(t, tx.QueryRow(ctx, `INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Audit fixture',gen_random_uuid()::text || '@fixture.invalid','fixture-only' FROM roles WHERE name='SUPER_ADMIN' RETURNING id`).Scan(&actor))
	batch, err := os.ReadFile("../../scripts/sql/redact-staff-audit-batch.sql")
	require.NoError(t, err)
	countSQL, err := os.ReadFile("../../scripts/sql/count-unsafe-staff-audits.sql")
	require.NoError(t, err)
	count := func() int64 { var n int64; require.NoError(t, tx.QueryRow(ctx, string(countSQL)).Scan(&n)); return n }
	apply := func() int64 { var n int64; require.NoError(t, tx.QueryRow(ctx, string(batch)).Scan(&n)); return n }
	type fixture struct {
		entity, action string
		input, want    *string
		dirty          bool
		id             int64
		metadata       string
	}
	str := func(value string) *string { return &value }
	fixtures := []fixture{
		{entity: "staff_user", action: "STAFF_CREATE", input: str(`{"ID":7,"RoleID":3,"Name":"Fixture","Email":"fixture@example.invalid","IsActive":true,"CreatedAt":"2026-11-03T01:00:00Z","PasswordHash":"fixture-hash","password_hash":"fixture-hash","password":"fixture-plain","CurrentPassword":"fixture-plain","refresh_token":"fixture-token","nested":{"PasswordHash":"fixture-hash"},"UpdatedAt":{"PasswordHash":"fixture-hash"}}`), want: str(`{"ID":7,"RoleID":3,"Name":"Fixture","Email":"fixture@example.invalid","IsActive":true,"CreatedAt":"2026-11-03T01:00:00Z"}`), dirty: true},
		{entity: "staff_user", action: "STAFF_UPDATE", input: str(`null`), dirty: true},
		{entity: "staff_user", action: "STAFF_UPDATE_STATUS", input: str(`["fixture-secret"]`), dirty: true},
		{entity: "staff_user", action: "STAFF_DELETE", input: str(`"fixture-secret"`), dirty: true},
		{entity: "staff_user", action: "STAFF_UPDATE_PASSWORD", input: str(`{"ID":7,"Name":"Fixture"}`), dirty: true},
		{entity: "staff_user", action: "STAFF_UPDATE", input: str(`{"ID":7,"RoleName":"ADMIN","Name":null}`), want: str(`{"ID":7,"RoleName":"ADMIN","Name":null}`)},
		{entity: "staff_user", action: "STAFF_DELETE"},
		{entity: "notification", action: "NOTIFICATION_UPDATE", input: str(`{"PasswordHash":"non-staff-fixture","nested":[1]}`), want: str(`{"PasswordHash":"non-staff-fixture","nested":[1]}`)},
	}
	var dirty int64
	for i := range fixtures {
		f := &fixtures[i]
		if f.dirty {
			dirty++
		}
		require.NoError(t, tx.QueryRow(ctx, `INSERT INTO audit_logs(actor_user_id,action,entity_type,entity_id,entity_name,before_json,after_json,ip_address,user_agent) VALUES($1,$2,$3,7,'Fixture',$4::jsonb,$4::jsonb,'127.0.0.1','fixture-agent') RETURNING id`, actor, f.action, f.entity, f.input).Scan(&f.id))
		require.NoError(t, tx.QueryRow(ctx, "SELECT (to_jsonb(a)-'before_json'-'after_json')::text FROM audit_logs a WHERE id=$1", f.id).Scan(&f.metadata))
	}
	require.Equal(t, dirty, count())
	_, err = tx.Exec(ctx, "SAVEPOINT dry_run")
	require.NoError(t, err)
	require.Equal(t, dirty, apply())
	require.Zero(t, count())
	_, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT dry_run")
	require.NoError(t, err)
	require.Equal(t, dirty, count(), "dry-run rollback must preserve original fixture state")
	require.Equal(t, dirty, apply())
	require.Zero(t, count())
	require.Zero(t, apply())
	for _, f := range fixtures {
		var before, after []byte
		var metadata string
		require.NoError(t, tx.QueryRow(ctx, "SELECT before_json,after_json,(to_jsonb(a)-'before_json'-'after_json')::text FROM audit_logs a WHERE id=$1", f.id).Scan(&before, &after, &metadata))
		require.JSONEq(t, f.metadata, metadata, "event metadata must remain unchanged")
		if f.want == nil {
			require.Nil(t, before)
			require.Nil(t, after)
		} else {
			require.JSONEq(t, *f.want, string(before))
			require.JSONEq(t, *f.want, string(after))
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_user_id,action,entity_type,before_json) SELECT $1,'STAFF_UPDATE','staff_user','{"ID":7,"PasswordHash":"fixture-hash"}'::jsonb FROM generate_series(1,503)`, actor)
	require.NoError(t, err)
	require.Equal(t, int64(503), count())
	require.Equal(t, int64(500), apply())
	require.Equal(t, int64(3), count())
	require.Equal(t, int64(3), apply())
	require.Zero(t, count())
	require.Zero(t, apply())
}

func TestStaffAuditRedactionSkipsLockedRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	var actor int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Locked audit',gen_random_uuid()::text || '@fixture.invalid','fixture-only' FROM roles WHERE name='SUPER_ADMIN' RETURNING id`).Scan(&actor))
	defer func() {
		_, err := pool.Exec(context.Background(), "DELETE FROM audit_logs WHERE actor_user_id=$1", actor)
		require.NoError(t, err)
		_, err = pool.Exec(context.Background(), "DELETE FROM staff_users WHERE id=$1", actor)
		require.NoError(t, err)
	}()
	var lockedID int64
	for i := 0; i < 2; i++ {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO audit_logs(actor_user_id,action,entity_type,before_json) VALUES($1,'STAFF_UPDATE','staff_user','{"ID":7,"PasswordHash":"fixture-hash"}') RETURNING id`, actor).Scan(&id))
		if i == 0 {
			lockedID = id
		}
	}
	lockTx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer lockTx.Rollback(context.Background())
	_, err = lockTx.Exec(ctx, "SELECT id FROM audit_logs WHERE id=$1 FOR UPDATE", lockedID)
	require.NoError(t, err)
	batch, err := os.ReadFile("../../scripts/sql/redact-staff-audit-batch.sql")
	require.NoError(t, err)
	countSQL, err := os.ReadFile("../../scripts/sql/count-unsafe-staff-audits.sql")
	require.NoError(t, err)
	var changed, remaining int64
	require.NoError(t, pool.QueryRow(ctx, string(batch)).Scan(&changed))
	require.Equal(t, int64(1), changed)
	require.NoError(t, pool.QueryRow(ctx, string(batch)).Scan(&changed))
	require.Zero(t, changed)
	require.NoError(t, pool.QueryRow(ctx, string(countSQL)).Scan(&remaining))
	require.Equal(t, int64(1), remaining, "zero changed is not completion while rows are locked")
	require.NoError(t, lockTx.Rollback(ctx))
	require.NoError(t, pool.QueryRow(ctx, string(batch)).Scan(&changed))
	require.Equal(t, int64(1), changed)
	require.NoError(t, pool.QueryRow(ctx, string(countSQL)).Scan(&remaining))
	require.Zero(t, remaining)
	// Verify the scrubbed row still has its useful non-secret state.
	var payload []byte
	require.NoError(t, pool.QueryRow(ctx, "SELECT before_json FROM audit_logs WHERE id=$1", lockedID).Scan(&payload))
	var state map[string]any
	require.NoError(t, json.Unmarshal(payload, &state))
	require.Equal(t, map[string]any{"ID": float64(7)}, state)
}
