//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

type retentionFixture struct {
	executionFixture
	operator *pgxpool.Pool
	callSQL  string
	args     sqlc.RunRetentionBatchParams
}

func newRetentionFixture(t *testing.T, ctx context.Context, class string) retentionFixture {
	t.Helper()
	f := retentionFixture{executionFixture: newExecutionFixture(t, ctx)}
	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	role := fmt.Sprintf("retention_%d", time.Now().UnixNano())
	login := pgx.Identifier{role}.Sanitize()
	qualified := pgx.Identifier{schema, "run_retention_batch"}.Sanitize()
	signature := qualified + "(uuid,uuid,uuid,uuid,text,integer,boolean)"
	executionSQL(t, ctx, f.executionFixture, "CREATE ROLE "+login+" LOGIN PASSWORD 'retention-fixture-only'")
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := f.pool.Exec(cleanup, "DROP OWNED BY "+login+"; DROP ROLE "+login)
		require.NoError(t, err)
	})
	executionSQL(t, ctx, f.executionFixture, "GRANT USAGE ON SCHEMA "+pgx.Identifier{schema}.Sanitize()+" TO "+login)
	cfg := f.pool.Config()
	cfg.ConnConfig.User, cfg.ConnConfig.Password = role, "retention-fixture-only"
	var err error
	f.operator, err = pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(f.operator.Close)
	f.callSQL = "SELECT outcome,quantity FROM " + qualified + "($1,$2,$3,$4,$5,$6,$7)"
	f.args = sqlc.RunRetentionBatchParams{GrantID: replayUUID(t, ctx, f.pool), ApplicationPublicID: f.project, RecordClass: class, BatchNo: 1}
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT origin_epoch,source_epoch FROM notification_runtime").Scan(&f.args.OriginEpoch, &f.args.SourceEpoch))
	_, err = f.call(ctx, f.args)
	requireRetentionDenied(t, err)
	executionSQL(t, ctx, f.executionFixture, "GRANT EXECUTE ON FUNCTION "+signature+" TO "+login)
	_, err = f.operator.Exec(ctx, "DELETE FROM mobile_origin_uses")
	requireRetentionDenied(t, err)
	_, err = f.operator.Exec(ctx, "UPDATE retention_policies SET held=false")
	requireRetentionDenied(t, err)
	executionSQL(t, ctx, f.executionFixture, "INSERT INTO retention_policies(application_id,record_class,policy_version,retain_after_expiry_seconds) VALUES($1,$2,gen_random_uuid(),30)", f.app, class)
	executionSQL(t, ctx, f.executionFixture, `INSERT INTO retention_grants(id,application_id,record_class,policy_version,operator_role,origin_epoch,source_epoch,
		cutoff_at,expires_at,batch_size,max_batches,allow_apply)
		SELECT $1,p.application_id,p.record_class,p.policy_version,$2::regrole::oid,r.origin_epoch,r.source_epoch,
			clock_timestamp()-interval '1 minute',clock_timestamp()+interval '1 hour',2,3,true
		FROM retention_policies p CROSS JOIN notification_runtime r`, f.args.GrantID, role)
	_, err = f.call(ctx, f.args)
	requireRetentionDenied(t, err)
	executionSQL(t, ctx, f.executionFixture, "UPDATE retention_policies SET held=false")
	return f
}

func requireRetentionDenied(t *testing.T, err error) {
	t.Helper()
	var p *pgconn.PgError
	require.ErrorAs(t, err, &p)
	require.Equal(t, "42501", p.Code)
}

func (f retentionFixture) call(ctx context.Context, a sqlc.RunRetentionBatchParams) (r sqlc.RunRetentionBatchRow, err error) {
	err = f.operator.QueryRow(ctx, f.callSQL, a.GrantID, a.ApplicationPublicID, a.OriginEpoch, a.SourceEpoch, a.RecordClass, a.BatchNo, a.Apply).Scan(&r.Outcome, &r.Quantity)
	return r, err
}

func (f retentionFixture) count(t *testing.T, ctx context.Context, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(ctx, query, args...).Scan(&n))
	return n
}

func TestRetentionNonceScopeCheckpointsAndHold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newRetentionFixture(t, ctx, "origin_nonce")
	var foreign pgtype.UUID
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO applications(key,name) VALUES('retention-other','Other') RETURNING public_id").Scan(&foreign))
	for i, project := range []pgtype.UUID{f.project, f.project, f.project, f.project, foreign} {
		expiry := time.Now().Add(-time.Hour)
		if i == 3 {
			expiry = time.Now().Add(time.Hour)
		}
		executionSQL(t, ctx, f.executionFixture, "INSERT INTO mobile_origin_uses VALUES($1,'issuer',$2,$3)", project, bytes.Repeat([]byte{byte(i)}, 32), expiry)
	}
	p := f.args
	p.Apply = true
	_, err := f.call(ctx, p)
	requireRetentionDenied(t, err)
	p.Apply = false
	first, err := f.call(ctx, p)
	require.NoError(t, err)
	require.Equal(t, sqlc.RunRetentionBatchRow{Outcome: "preview", Quantity: 2}, first)
	require.Equal(t, 5, f.count(t, ctx, "SELECT count(*) FROM mobile_origin_uses"))
	for _, mutate := range []func(*sqlc.RunRetentionBatchParams){
		func(p *sqlc.RunRetentionBatchParams) { p.ApplicationPublicID = foreign }, func(p *sqlc.RunRetentionBatchParams) { p.SourceEpoch = foreign },
		func(p *sqlc.RunRetentionBatchParams) { p.RecordClass = "receipt_request" }, func(p *sqlc.RunRetentionBatchParams) { p.BatchNo = 2 },
	} {
		bad := p
		mutate(&bad)
		_, err := f.call(ctx, bad)
		requireRetentionDenied(t, err)
	}

	p.Apply = true
	executionSQL(t, ctx, f.executionFixture, "UPDATE retention_policies SET held=true")
	_, err = f.call(ctx, p)
	requireRetentionDenied(t, err)
	executionSQL(t, ctx, f.executionFixture, "UPDATE retention_policies SET held=false; UPDATE notification_runtime SET sealed=true")
	_, err = f.call(ctx, p)
	requireRetentionDenied(t, err)
	executionSQL(t, ctx, f.executionFixture, "UPDATE notification_runtime SET sealed=false")

	// No partial delete on evidence failure, then two replicas retry the same checkpoint.
	executionSQL(t, ctx, f.executionFixture, "ALTER TABLE retention_batches ADD CONSTRAINT fixture_audit_failure CHECK (applied_at IS NULL)")
	_, err = f.call(ctx, p)
	require.Error(t, err)
	require.Equal(t, 5, f.count(t, ctx, "SELECT count(*) FROM mobile_origin_uses"))
	executionSQL(t, ctx, f.executionFixture, "ALTER TABLE retention_batches DROP CONSTRAINT fixture_audit_failure")
	var wg sync.WaitGroup
	type attempt struct {
		r   sqlc.RunRetentionBatchRow
		err error
	}
	out := make(chan attempt, 2)
	for range 2 {
		wg.Go(func() { r, err := f.call(ctx, p); out <- attempt{r, err} })
	}
	wg.Wait()
	close(out)
	for a := range out {
		require.NoError(t, a.err)
		require.Equal(t, sqlc.RunRetentionBatchRow{Outcome: "more", Quantity: 2}, a.r)
	}
	require.Equal(t, 3, f.count(t, ctx, "SELECT count(*) FROM mobile_origin_uses"))
	p.BatchNo = 2
	p.Apply = false
	r, err := f.call(ctx, p)
	require.NoError(t, err)
	require.EqualValues(t, 1, r.Quantity)
	p.Apply = true
	r, err = f.call(ctx, p)
	require.NoError(t, err)
	require.Equal(t, sqlc.RunRetentionBatchRow{Outcome: "complete", Quantity: 1}, r)
	require.Equal(t, 2, f.count(t, ctx, "SELECT count(*) FROM mobile_origin_uses"))
	require.Equal(t, 1, f.count(t, ctx, "SELECT count(*) FROM mobile_origin_uses WHERE application_public_id=$1", foreign))
	executionSQL(t, ctx, f.executionFixture, "UPDATE retention_grants SET revoked_at=clock_timestamp()")
	_, err = f.call(ctx, p)
	requireRetentionDenied(t, err)
}

func TestRetentionReceiptExpiryPreservesEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newRetentionFixture(t, ctx, "receipt_request")
	old, live := time.Now().Add(-2*time.Hour).Unix(), time.Now().Add(time.Hour).Unix()
	for i, expiry := range []int64{old, live} {
		executionSQL(t, ctx, f.executionFixture, `INSERT INTO notification_receipt_metadata(delivery_id,application_id,version,key_id,environment,expires_unix)
			VALUES($1,$2,1,'fixture','fixture',$3)`, f.deliveries[i], f.app, expiry)
		executionSQL(t, ctx, f.executionFixture, `INSERT INTO notification_receipt_requests VALUES($1,$2,gen_random_uuid(),$3,$4,$5)`, f.app, f.user, f.deliveries[i], bytes.Repeat([]byte{4}, 32), expiry)
	}
	executionSQL(t, ctx, f.executionFixture, "INSERT INTO notification_observations(application_id,delivery_id,event,received_at) VALUES($1,$2,'OPENED',clock_timestamp())", f.app, f.deliveries[0])
	// A corrupted marker with an earlier expiry cannot erase still-valid request conflict evidence.
	executionSQL(t, ctx, f.executionFixture, "UPDATE notification_receipt_requests SET expires_unix=$1 WHERE delivery_id=$2", old, f.deliveries[1])
	_, err := f.call(ctx, f.args)
	require.Error(t, err)
	require.Equal(t, 2, f.count(t, ctx, "SELECT count(*) FROM notification_receipt_requests"))
	executionSQL(t, ctx, f.executionFixture, "UPDATE notification_receipt_requests SET expires_unix=$1 WHERE delivery_id=$2", live, f.deliveries[1])
	r, err := f.call(ctx, f.args)
	require.NoError(t, err)
	require.EqualValues(t, 1, r.Quantity)
	f.args.Apply = true
	r, err = f.call(ctx, f.args)
	require.NoError(t, err)
	require.Equal(t, "complete", r.Outcome)
	require.Equal(t, 1, f.count(t, ctx, "SELECT count(*) FROM notification_receipt_requests WHERE expires_unix=$1", live))
	require.Equal(t, 2, f.count(t, ctx, "SELECT count(*) FROM notification_delivery_intents WHERE status='PENDING'"))
	require.Equal(t, 2, f.count(t, ctx, "SELECT count(*) FROM notification_receipt_metadata"))
	require.Equal(t, 1, f.count(t, ctx, "SELECT count(*) FROM notification_observations"))
	down, err := os.ReadFile("../../db/migrations/000032_create_retention_jobs.down.sql")
	require.NoError(t, err)
	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	_, err = f.pool.Exec(ctx, strings.ReplaceAll(string(down), "public.", pgx.Identifier{schema}.Sanitize()+"."))
	var pe *pgconn.PgError
	require.ErrorAs(t, err, &pe)
	require.Equal(t, "23514", pe.Code)
}
