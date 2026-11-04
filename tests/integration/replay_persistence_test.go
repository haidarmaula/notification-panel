//go:build integration

package integration

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
)

type replayFixture struct {
	pool                 *pgxpool.Pool
	q                    *sqlc.Queries
	app, staff, campaign int64
	source, epoch        pgtype.UUID
}

func newReplayFixture(t *testing.T, ctx context.Context) replayFixture {
	t.Helper()
	pool := acceptanceFixture(t, ctx)
	f := replayFixture{pool: pool, q: sqlc.New(pool)}
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO applications(key,name) VALUES('replay-fixture','Replay fixture') RETURNING id").Scan(&f.app))
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO staff_users(role_id,name,email,password_hash) VALUES((SELECT id FROM roles WHERE name='ADMIN'),'Replay operator','replay@example.invalid','fixture') RETURNING id").Scan(&f.staff))
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO notifications(application_id,title,body,created_by) VALUES($1,'Replay','Fixture',$2) RETURNING id", f.app, f.staff).Scan(&f.campaign))
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation) VALUES(gen_random_uuid(),'send','fixture','incarnation') RETURNING id").Scan(&f.source))
	require.NoError(t, pool.QueryRow(ctx, "SELECT gen_random_uuid()").Scan(&f.epoch))
	return f
}

func replayUUID(t *testing.T, ctx context.Context, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, "SELECT gen_random_uuid()").Scan(&id))
	return id
}

func (f replayFixture) request(t *testing.T, ctx context.Context) pgtype.UUID {
	t.Helper()
	id := replayUUID(t, ctx, f.pool)
	n, err := f.q.InsertReplayRequest(ctx, sqlc.InsertReplayRequestParams{ApplicationID: f.app, ID: id, StaffUserID: f.staff, OperationalGrantID: f.epoch, SelectionHash: bytes.Repeat([]byte{1}, 32), ChangeReference: "INC-123"})
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	return id
}

func (f replayFixture) evidence(t *testing.T, ctx context.Context, offset int64) {
	t.Helper()
	_, err := f.pool.Exec(ctx, `INSERT INTO notification_send_dead_letters(log_source_id,original_partition,original_offset,original_key,original_payload,reason,attempts)
 VALUES($1,0,$2,'','', 'invalid_envelope',1)`, f.source, offset)
	require.NoError(t, err)
	require.NoError(t, f.q.InsertReplayControl(ctx, sqlc.InsertReplayControlParams{LogSourceID: f.source, OriginalOffset: offset}))
}

func (f replayFixture) attempt(t *testing.T, ctx context.Context, request pgtype.UUID, offset int64) sqlc.NotificationReplayAttempt {
	t.Helper()
	row, err := f.q.InsertReplayAttempt(ctx, sqlc.InsertReplayAttemptParams{ApplicationID: f.app, RequestID: request, LogSourceID: f.source, OriginalOffset: offset, NotificationID: pgtype.Int8{Int64: f.campaign, Valid: true}, OriginEpoch: f.epoch, SourceEpoch: f.epoch, ClaimToken: replayUUID(t, ctx, f.pool), Outcome: "started"})
	require.NoError(t, err)
	return row
}

func replayClaim(row sqlc.NotificationReplayAttempt) sqlc.ClaimReplayControlParams {
	return sqlc.ClaimReplayControlParams{ApplicationID: pgtype.Int8{Int64: row.ApplicationID, Valid: true}, RequestID: row.RequestID, LogSourceID: row.LogSourceID, OriginalPartition: row.OriginalPartition, OriginalOffset: row.OriginalOffset, ClaimToken: row.ClaimToken}
}

func replayFinish(row sqlc.NotificationReplayAttempt, outcome string) sqlc.FinishReplayAttemptParams {
	return sqlc.FinishReplayAttemptParams{ApplicationID: row.ApplicationID, RequestID: row.RequestID, LogSourceID: row.LogSourceID, OriginalPartition: row.OriginalPartition, OriginalOffset: row.OriginalOffset, ClaimToken: row.ClaimToken, Outcome: outcome}
}

func TestReplayPersistence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newReplayFixture(t, ctx)
	request := f.request(t, ctx)
	f.evidence(t, ctx, 42)
	for _, outcome := range []string{"published", "publish_unconfirmed", "abandoned_unknown"} {
		_, err := f.q.InsertReplayAttempt(ctx, sqlc.InsertReplayAttemptParams{
			ApplicationID: f.app, RequestID: request, LogSourceID: f.source, OriginalOffset: 42,
			NotificationID: pgtype.Int8{Int64: f.campaign, Valid: true}, OriginEpoch: f.epoch,
			SourceEpoch: f.epoch, ClaimToken: replayUUID(t, ctx, f.pool), Outcome: outcome,
		})
		require.Error(t, err, "final publication/cleanup outcomes require a prior fenced attempt: %s", outcome)
	}
	first := f.attempt(t, ctx, request, 42)
	second := f.attempt(t, ctx, f.request(t, ctx), 42)
	n, err := f.q.ClaimReplayControl(ctx, replayClaim(first))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = f.q.ClaimReplayControl(ctx, replayClaim(second))
	require.NoError(t, err)
	require.Zero(t, n, "another request cannot take a live position")
	n, err = f.q.FinishReplayAttempt(ctx, replayFinish(second, "published"))
	require.NoError(t, err)
	require.Zero(t, n, "an unowned attempt cannot finish")
	wrong := replayFinish(first, "published")
	wrong.ClaimToken = second.ClaimToken
	n, err = f.q.FinishReplayAttempt(ctx, wrong)
	require.NoError(t, err)
	require.Zero(t, n)

	tx, err := f.pool.BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() {
		cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancelCleanup()
		_ = tx.Rollback(cleanup)
	}()
	n, err = sqlc.New(tx).FinishReplayAttempt(ctx, replayFinish(first, "published"))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.NoError(t, tx.Rollback(ctx))
	var outcome string
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT outcome FROM notification_replay_attempts WHERE request_id=$1", request).Scan(&outcome))
	require.Equal(t, "started", outcome, "a failed result transaction preserves claim uncertainty")
	n, err = f.q.FinishReplayAttempt(ctx, replayFinish(first, "published"))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	resolve := sqlc.ResolveReplayControlParams{ApplicationID: pgtype.Int8{Int64: f.app, Valid: true}, RequestID: request, Outcome: pgtype.Text{String: "published", Valid: true}, LogSourceID: f.source, OriginalOffset: 42, ExpectedToken: second.ClaimToken}
	n, err = f.q.ResolveReplayControl(ctx, resolve)
	require.NoError(t, err)
	require.Zero(t, n)
	resolve.ExpectedToken = pgtype.UUID{}
	n, err = f.q.ResolveReplayControl(ctx, resolve)
	require.NoError(t, err)
	require.Zero(t, n, "publication resolution requires the owned claim token")
	resolve.ExpectedToken = first.ClaimToken
	n, err = f.q.ResolveReplayControl(ctx, resolve)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = f.q.ClaimReplayControl(ctx, replayClaim(second))
	require.NoError(t, err)
	require.Zero(t, n, "resolution is not a resend permission")

	for _, sql := range []string{
		"UPDATE notification_replay_requests SET selection_hash=decode(repeat('cd',32),'hex')",
		"UPDATE notification_replay_attempts SET outcome='held' WHERE outcome='published'",
		"UPDATE notification_replay_attempts SET lease_expires_at=lease_expires_at+interval '1 second'",
		"UPDATE notification_replay_controls SET resolved_application_id=NULL,resolved_request_id=NULL,resolved_outcome=NULL",
	} {
		_, err = f.pool.Exec(ctx, sql)
		require.Error(t, err, sql)
	}
	var foreignApp int64
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO applications(key,name) VALUES('foreign-app','Foreign') RETURNING id").Scan(&foreignApp))
	_, err = f.pool.Exec(ctx, `INSERT INTO notification_replay_requests(application_id,id,staff_user_id,operational_grant_id,selection_hash,change_reference)
 VALUES($1,$2,$3,$4,decode(repeat('ab',32),'hex'),'INC-456')`, foreignApp, request, f.staff, f.epoch)
	require.NoError(t, err, "request namespace is explicitly per app")
	_, err = f.q.InsertReplayAttempt(ctx, sqlc.InsertReplayAttemptParams{ApplicationID: foreignApp, RequestID: request, LogSourceID: f.source, OriginalOffset: 42, NotificationID: pgtype.Int8{Int64: f.campaign, Valid: true}, OriginEpoch: f.epoch, SourceEpoch: f.epoch, ClaimToken: replayUUID(t, ctx, f.pool), Outcome: "started"})
	require.Error(t, err, "composite FK rejects a foreign campaign even when its numeric ID exists")

	// Adapt only explicit public qualifications for this isolated test schema.
	down, err := os.ReadFile("../../db/migrations/000025_create_replay_persistence.down.sql")
	require.NoError(t, err)
	schema := f.pool.Config().ConnConfig.RuntimeParams["search_path"]
	schema = strings.TrimSuffix(schema, ",pg_catalog")
	_, err = f.pool.Exec(ctx, strings.ReplaceAll(string(down), "public.", pgx.Identifier{schema}.Sanitize()+"."))
	require.Error(t, err, "down migration must refuse retained requests/attempts/resolutions")
}

func TestReplayClaimConcurrencyAndExpiry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newReplayFixture(t, ctx)
	f.evidence(t, ctx, 1)
	first := f.attempt(t, ctx, f.request(t, ctx), 1)
	second := f.attempt(t, ctx, f.request(t, ctx), 1)
	var wg sync.WaitGroup
	type result struct {
		n   int64
		err error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, row := range []sqlc.NotificationReplayAttempt{first, second} {
		wg.Add(1)
		go func(row sqlc.NotificationReplayAttempt) {
			defer wg.Done()
			<-start
			n, err := f.q.ClaimReplayControl(ctx, replayClaim(row))
			results <- result{n: n, err: err}
		}(row)
	}
	close(start)
	wg.Wait()
	close(results)
	var wins int64
	for r := range results {
		require.NoError(t, r.err)
		wins += r.n
	}
	require.EqualValues(t, 1, wins, "two racing requests cannot both claim one physical source position")

	f.evidence(t, ctx, 2)
	request := f.request(t, ctx)
	token := replayUUID(t, ctx, f.pool)
	_, err := f.pool.Exec(ctx, `INSERT INTO notification_replay_attempts(application_id,request_id,log_source_id,original_partition,original_offset,notification_id,origin_epoch,source_epoch,claim_token,created_at,lease_expires_at,outcome)
 VALUES($1,$2,$3,0,2,$4,$5,$5,$6,clock_timestamp()-interval '31 seconds',clock_timestamp()-interval '1 second','started')`, f.app, request, f.source, f.campaign, f.epoch, token)
	require.NoError(t, err)
	expired, err := f.q.GetReplayAttempt(ctx, sqlc.GetReplayAttemptParams{ApplicationID: f.app, RequestID: request, LogSourceID: f.source, OriginalOffset: 2})
	require.NoError(t, err)
	n, err := f.q.ClaimReplayControl(ctx, replayClaim(expired))
	require.NoError(t, err)
	require.Zero(t, n, "expired lease cannot become a new claim")
	// Reproduce a claim left by a dead process; its original lease is immutable.
	_, err = f.pool.Exec(ctx, `UPDATE notification_replay_controls SET active_application_id=$1,active_request_id=$2,active_claim_token=$3 WHERE original_offset=2`, f.app, request, token)
	require.NoError(t, err)
	rows, err := f.q.ListExpiredReplayClaims(ctx, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	n, err = f.q.FinishReplayAttempt(ctx, replayFinish(expired, "published"))
	require.NoError(t, err)
	require.Zero(t, n, "an expired worker cannot finalize publication")
	n, err = f.q.AbandonExpiredReplayAttempt(ctx, sqlc.AbandonExpiredReplayAttemptParams{ApplicationID: f.app, RequestID: request, LogSourceID: f.source, OriginalOffset: 2, ClaimToken: token})
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = f.q.ClearFinishedReplayClaim(ctx, sqlc.ClearFinishedReplayClaimParams{ApplicationID: f.app, RequestID: request, LogSourceID: f.source, OriginalOffset: 2, ClaimToken: token})
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	successor := f.attempt(t, ctx, f.request(t, ctx), 2)
	n, err = f.q.ClaimReplayControl(ctx, replayClaim(successor))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = f.q.FinishReplayAttempt(ctx, replayFinish(expired, "published"))
	require.NoError(t, err)
	require.Zero(t, n, "a stale finalizer cannot overwrite its successor")
}
