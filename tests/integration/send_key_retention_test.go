//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"hello/internal/database/sqlc"
	"hello/internal/dispatch"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func (f retentionFixture) nextSendGrant(t *testing.T, ctx context.Context, expired bool) sqlc.RunRetentionBatchParams {
	t.Helper()
	p := f.args
	p.GrantID = replayUUID(t, ctx, f.pool)
	executionSQL(t, ctx, f.executionFixture, `INSERT INTO retention_grants(id,application_id,record_class,policy_version,operator_role,
		origin_epoch,source_epoch,cutoff_at,issued_at,expires_at,batch_size,max_batches,allow_apply)
		SELECT $1,application_id,record_class,policy_version,operator_role,origin_epoch,source_epoch,
		CASE WHEN $3 THEN clock_timestamp()-interval '3 hours' ELSE cutoff_at END,
		CASE WHEN $3 THEN clock_timestamp()-interval '2 hours' ELSE issued_at END,
		CASE WHEN $3 THEN clock_timestamp()-interval '1 hour' ELSE expires_at END,batch_size,max_batches,allow_apply
		FROM retention_grants WHERE id=$2`, p.GrantID, f.args.GrantID, expired)
	return p
}

func (f retentionFixture) insertSendKey(t *testing.T, ctx context.Context, campaign int64) {
	t.Helper()
	executionSQL(t, ctx, f.executionFixture, `INSERT INTO notification_send_keys(application_id,staff_user_id,key_source,key,
		notification_id,queued_at,deadline_at,expires_at)
		SELECT n.application_id,$2,'derived',n.id::text,n.id,n.queued_at,n.deadline_at,n.deadline_at FROM notifications n WHERE n.id=$1`, campaign, f.staff)
}

func (f retentionFixture) terminalHistory(t *testing.T, ctx context.Context) {
	t.Helper()
	executionSQL(t, ctx, f.executionFixture, `UPDATE notifications SET status='COMPLETED',queued_at=clock_timestamp()-interval '5 hours',
		deadline_at=clock_timestamp()-interval '4 hours',completed_at=clock_timestamp()-interval '4 hours' WHERE id=$1`, f.campaign)
	executionSQL(t, ctx, f.executionFixture, "UPDATE notification_delivery_intents SET status='SENT' WHERE notification_id=$1", f.campaign)
	f.insertSendKey(t, ctx, f.campaign)
}

// Use a non-login function owner and column-level grants from the runbook.
// This separately exercises the operator login and the definer privilege boundary.
func (f retentionFixture) restrictRetentionOwner(t *testing.T, ctx context.Context, send bool) {
	t.Helper()
	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	ident := pgx.Identifier{schema}.Sanitize()
	owner := pgx.Identifier{fmt.Sprintf("retention_definer_%d", time.Now().UnixNano())}.Sanitize()
	var originalName string
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT current_user").Scan(&originalName))
	original := pgx.Identifier{originalName}.Sanitize()
	fn := pgx.Identifier{schema, "run_retention_batch"}.Sanitize() + "(uuid,uuid,uuid,uuid,text,integer,boolean)"
	exec := func(s string) { t.Helper(); executionSQL(t, ctx, f.executionFixture, s) }
	exec("CREATE ROLE " + owner + " NOLOGIN")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := f.pool.Exec(ctx, "ALTER FUNCTION "+fn+" OWNER TO "+original+"; DROP OWNED BY "+owner+"; DROP ROLE "+owner)
		require.NoError(t, err)
	})
	exec("GRANT USAGE, CREATE ON SCHEMA " + ident + " TO " + owner)
	exec("ALTER FUNCTION " + fn + " OWNER TO " + owner)
	exec("REVOKE CREATE ON SCHEMA " + ident + " FROM " + owner)
	for _, permission := range []string{
		"SELECT ON notification_runtime,applications,retention_policies,retention_grants,mobile_origin_uses",
		"UPDATE(singleton) ON notification_runtime", "UPDATE(id) ON applications", "UPDATE(application_id) ON retention_policies", "UPDATE(id) ON retention_grants",
		"SELECT(grant_id,batch_no,preview_count,preview_at,outcome,removed_count,applied_at) ON retention_batches",
		"INSERT(grant_id,batch_no,preview_count,outcome) ON retention_batches", "UPDATE(applied_at,removed_count,outcome) ON retention_batches",
		"DELETE ON mobile_origin_uses",
	} {
		exec("GRANT " + permission + " TO " + owner)
	}
	if send {
		for _, permission := range []string{
			"SELECT, DELETE ON notification_send_keys",
			"SELECT(id,application_id,queued_at,deadline_at,completed_at,origin_epoch,source_epoch,status,lease_owner,lease_until) ON notifications",
			"SELECT(application_id,notification_id,origin_epoch,source_epoch) ON notification_preparations",
			"SELECT(notification_id,status,attempt_id,attempt_resolved_at) ON notification_delivery_intents",
			"SELECT(notification_id) ON notification_send_outbox", "SELECT(application_id,notification_id) ON notification_update_outbox",
			"SELECT(original_key,log_source_id,original_partition,original_offset) ON notification_send_dead_letters",
			"SELECT ON notification_replay_attempts,notification_replay_controls",
			"SELECT(send_candidates,send_page_full,send_page_end), INSERT(send_candidates,send_page_full,send_page_end) ON retention_batches",
		} {
			exec("GRANT " + permission + " TO " + owner)
		}
	}
}

func TestSendKeyRetentionMigrationKeepsMobileGrant(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newRetentionFixture(t, ctx, "origin_nonce")
	f.restrictRetentionOwner(t, ctx, false)
	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	migrate := func(direction string) {
		t.Helper()
		body, err := os.ReadFile("../../db/migrations/000036_send_key_retention." + direction + ".sql")
		require.NoError(t, err)
		_, err = f.pool.Exec(ctx, strings.ReplaceAll(string(body), "public.", pgx.Identifier{schema}.Sanitize()+"."))
		require.NoError(t, err)
	}
	authority := func() string {
		t.Helper()
		var signature string
		require.NoError(t, f.pool.QueryRow(ctx, `SELECT ROW(p.oid,p.proowner,p.proacl)::text FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
            WHERE n.nspname=$1 AND p.proname='run_retention_batch'`, schema).Scan(&signature))
		return signature
	}
	original := authority()
	migrate("down")
	require.Equal(t, original, authority())
	p := f.args
	r, err := f.call(ctx, p)
	require.NoError(t, err)
	require.Equal(t, "preview", r.Outcome)
	migrate("up")
	require.Equal(t, original, authority())
	p.Apply = true
	r, err = f.call(ctx, p)
	require.NoError(t, err)
	require.Equal(t, "complete", r.Outcome)
}

func TestSendKeyRetentionDependencies(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	f := newRetentionFixture(t, ctx, "send_key")
	f.restrictRetentionOwner(t, ctx, true)
	f.terminalHistory(t, ctx)
	exec := func(query string, args ...any) { t.Helper(); executionSQL(t, ctx, f.executionFixture, query, args...) }
	preview := func(want int32) {
		t.Helper()
		p := f.nextSendGrant(t, ctx, false)
		r, err := f.call(ctx, p)
		require.NoError(t, err)
		require.EqualValues(t, want, r.Quantity)
	}
	preview(1)
	for _, status := range []string{"SCHEDULED", "QUEUED", "PROCESSING", "DRAFT", "CANCELLED"} {
		exec("UPDATE notifications SET status=$1 WHERE id=$2", status, f.campaign)
		preview(0)
	}
	exec("UPDATE notifications SET status='FAILED' WHERE id=$1", f.campaign)
	preview(1)
	exec("UPDATE notifications SET lease_owner=gen_random_uuid(),lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", f.campaign)
	preview(0)
	exec("UPDATE notifications SET lease_owner=NULL,lease_until=NULL WHERE id=$1", f.campaign)
	exec("UPDATE notification_delivery_intents SET status='PENDING' WHERE id=$1", f.deliveries[0])
	preview(0)
	exec("UPDATE notification_delivery_intents SET status='OPENED',attempt_count=1, attempt_id=gen_random_uuid(),claim_generation=1,last_attempt_at=clock_timestamp() WHERE id=$1", f.deliveries[0])
	preview(0)
	exec("UPDATE notification_delivery_intents SET attempt_resolved_at=clock_timestamp() WHERE id=$1", f.deliveries[0])
	exec("UPDATE notification_send_keys SET expires_at=clock_timestamp()+interval '1 hour'")
	preview(0)
	exec("UPDATE notification_send_keys SET expires_at=clock_timestamp()-interval '10 seconds'")
	preview(0)
	exec("UPDATE notification_send_keys SET expires_at=deadline_at")
	exec("UPDATE retention_policies SET retain_after_expiry_seconds=3600 WHERE record_class='send_key'")
	_, err := f.call(ctx, f.args)
	requireRetentionDenied(t, err)
	exec("UPDATE retention_policies SET retain_after_expiry_seconds=30 WHERE record_class='send_key'")
	exec(`INSERT INTO notification_send_outbox(application_id,notification_id,requested_at,deadline_at,origin_epoch,source_epoch)
		SELECT application_id,id,queued_at,deadline_at,origin_epoch,source_epoch FROM notifications WHERE id=$1`, f.campaign)
	preview(0)
	var outboxID int64
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT id FROM notification_send_outbox WHERE notification_id=$1", f.campaign).Scan(&outboxID))
	deleted, err := f.q.DeletePublishedSendOutbox(ctx, outboxID)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	eventID := replayUUID(t, ctx, f.pool)
	exec(`INSERT INTO notification_update_outbox(event_id,application_id,notification_id,delivery_id,delivery_version,origin_epoch,source_epoch,payload,occurred_at)
		SELECT $1,application_id,id,$2,1,origin_epoch,source_epoch,'fixture',clock_timestamp() FROM notifications WHERE id=$3`, eventID, f.deliveries[0], f.campaign)
	preview(0)
	deleted, err = f.q.DeletePublishedDeliveryUpdate(ctx, eventID)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	preview(1)

	p := f.nextSendGrant(t, ctx, true)
	_, err = f.call(ctx, p)
	requireRetentionDenied(t, err)
	p = f.args
	r, err := f.call(ctx, p)
	require.NoError(t, err)
	require.EqualValues(t, 1, r.Quantity)
	p.Apply = true
	for _, hold := range []struct{ on, off string }{
		{"UPDATE retention_policies SET held=true", "UPDATE retention_policies SET held=false"},
		{"UPDATE notification_runtime SET sealed=true", "UPDATE notification_runtime SET sealed=false"},
		{"UPDATE notification_runtime SET migration_hold=true", "UPDATE notification_runtime SET migration_hold=false"},
	} {
		exec(hold.on)
		_, err = f.call(ctx, p)
		requireRetentionDenied(t, err)
		exec(hold.off)
	}
	// The maintenance login can only run the granted function.
	_, err = f.operator.Exec(ctx, "DELETE FROM notification_send_keys")
	requireRetentionDenied(t, err)
	r, err = f.call(ctx, p)
	require.NoError(t, err)
	require.Equal(t, sqlc.RunRetentionBatchRow{Outcome: "complete", Quantity: 1}, r)
	require.Zero(t, f.count(t, ctx, "SELECT count(*) FROM notification_send_keys"))
	require.Equal(t, 2, f.count(t, ctx, "SELECT count(*) FROM notification_delivery_intents"))
	require.Equal(t, 1, f.count(t, ctx, "SELECT count(*) FROM notification_preparations"))
	require.Equal(t, 1, f.count(t, ctx, "SELECT count(*) FROM notifications WHERE id=$1 AND status='FAILED'", f.campaign))
	down, err := os.ReadFile("../../db/migrations/000036_send_key_retention.down.sql")
	require.NoError(t, err)
	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	_, err = f.pool.Exec(ctx, strings.ReplaceAll(string(down), "public.", pgx.Identifier{schema}.Sanitize()+"."))
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23514", pgErr.Code)
}

func TestSendKeyRetentionQuarantineRace(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newRetentionFixture(t, ctx, "send_key")
	f.restrictRetentionOwner(t, ctx, true)
	f.terminalHistory(t, ctx)
	p := f.args
	r, err := f.call(ctx, p)
	require.NoError(t, err)
	require.EqualValues(t, 1, r.Quantity)
	p.Apply = true
	// Pause the production quarantine transaction after INSERT, before its
	// commit. The retention apply must wait before taking its dependency snapshot.
	db := &pauseDeviceCommit{Pool: f.pool, reached: make(chan struct{}), release: make(chan struct{})}
	store, err := dispatch.NewQuarantine(db)
	require.NoError(t, err)
	db.next.Store(true)
	done := make(chan error, 1)
	go func() {
		done <- store.Store(ctx, dispatch.SourceMessage{LogSourceID: f.source, Topic: "send", Offset: 42,
			Key: []byte("notification_" + strconv.FormatInt(f.campaign, 10)), Payload: []byte("malformed but attributable key")}, dispatch.InvalidEnvelope, 1)
	}()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-db.reached:
	}
	_, blocked := f.call(ctx, p)
	close(db.release)
	require.NoError(t, <-done)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, blocked, &pgErr)
	require.Equal(t, "55P03", pgErr.Code)
	r, err = f.call(ctx, p)
	require.NoError(t, err)
	require.EqualValues(t, 0, r.Quantity)
	require.Equal(t, 1, f.count(t, ctx, "SELECT count(*) FROM notification_send_keys"))

	// A separately authorized resolution links physical position, lineage and
	// campaign. A started or merely published attempt cannot settle that position.
	request := f.request(t, ctx)
	require.NoError(t, f.q.InsertReplayControl(ctx, sqlc.InsertReplayControlParams{LogSourceID: f.source, OriginalOffset: 42}))
	attempt, err := f.q.InsertReplayAttempt(ctx, sqlc.InsertReplayAttemptParams{ApplicationID: f.app, RequestID: request, LogSourceID: f.source, OriginalOffset: 42,
		NotificationID: pgtype.Int8{Int64: f.campaign, Valid: true}, OriginEpoch: p.OriginEpoch, SourceEpoch: p.SourceEpoch, ClaimToken: replayUUID(t, ctx, f.pool), Outcome: "started"})
	require.NoError(t, err)
	n, err := f.q.ClaimReplayControl(ctx, replayClaim(attempt))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	r, err = f.call(ctx, f.nextSendGrant(t, ctx, false))
	require.NoError(t, err)
	require.Zero(t, r.Quantity)
	n, err = f.q.FinishReplayAttempt(ctx, replayFinish(attempt, "published"))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	r, err = f.call(ctx, f.nextSendGrant(t, ctx, false))
	require.NoError(t, err)
	require.Zero(t, r.Quantity)
	n, err = f.q.ResolveReplayControl(ctx, sqlc.ResolveReplayControlParams{ApplicationID: pgtype.Int8{Int64: f.app, Valid: true}, RequestID: request, LogSourceID: f.source,
		OriginalOffset: 42, ExpectedToken: attempt.ClaimToken, Outcome: pgtype.Text{String: "published", Valid: true}})
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	next := f.nextSendGrant(t, ctx, false)
	r, err = f.call(ctx, next)
	require.NoError(t, err)
	require.EqualValues(t, 1, r.Quantity)
	next.Apply = true
	r, err = f.call(ctx, next)
	require.NoError(t, err)
	require.EqualValues(t, 1, r.Quantity)
	require.Equal(t, 1, f.count(t, ctx, "SELECT count(*) FROM notification_send_dead_letters"))
	require.Equal(t, 1, f.count(t, ctx, "SELECT count(*) FROM notification_replay_controls WHERE resolved_outcome='published'"))
	require.Equal(t, 1, f.count(t, ctx, "SELECT count(*) FROM notification_replay_attempts WHERE outcome='published'"))
}

func TestSendKeyRetentionPageAdvancesPastBlockedKeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newRetentionFixture(t, ctx, "send_key")
	var ids []int64
	for _, status := range []string{"PROCESSING", "COMPLETED", "SCHEDULED", "FAILED", "COMPLETED"} {
		var id int64
		require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notifications(application_id,title,body,created_by,status,
			queued_at,deadline_at,completed_at,origin_epoch,source_epoch) SELECT $1,'fixture','fixture',$2,$3,clock_timestamp()-interval '5 hours',
			clock_timestamp()-interval '4 hours',clock_timestamp()-interval '3 hours',origin_epoch,source_epoch FROM notification_runtime RETURNING id`, f.app, f.staff, status).Scan(&id))
		executionSQL(t, ctx, f.executionFixture, `INSERT INTO notification_preparations(notification_id,application_id,origin_epoch,source_epoch,resolved_user_count,eligible_user_count,eligible_device_count)
			SELECT id,application_id,origin_epoch,source_epoch,0,0,0 FROM notifications WHERE id=$1`, id)
		f.insertSendKey(t, ctx, id)
		ids = append(ids, id)
	}
	for batch := int32(1); batch <= 3; batch++ {
		p := f.args
		p.BatchNo = batch
		r, err := f.call(ctx, p)
		require.NoError(t, err)
		require.Equal(t, sqlc.RunRetentionBatchRow{Outcome: "preview", Quantity: 1}, r)
		if batch == 1 {
			// Becoming eligible after review must not expand that page's permitted IDs.
			executionSQL(t, ctx, f.executionFixture, "UPDATE notifications SET status='COMPLETED' WHERE id=$1", ids[0])
		}
		p.Apply = true
		r, err = f.call(ctx, p)
		require.NoError(t, err)
		want := "more"
		if batch == 3 {
			want = "complete"
		}
		require.Equal(t, sqlc.RunRetentionBatchRow{Outcome: want, Quantity: 1}, r)
		replayed, err := f.call(ctx, p)
		require.NoError(t, err)
		require.Equal(t, r, replayed)
	}
	require.Equal(t, 2, f.count(t, ctx, "SELECT count(*) FROM notification_send_keys"))
	require.Equal(t, 2, f.count(t, ctx, "SELECT count(*) FROM notification_send_keys WHERE notification_id=ANY($1)", []int64{ids[0], ids[2]}))
}
