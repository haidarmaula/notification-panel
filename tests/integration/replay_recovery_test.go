//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
)

func seedExpiredReplayClaim(t *testing.T, ctx context.Context, f replayFixture, offset int64) {
	t.Helper()
	f.evidence(t, ctx, offset)
	request := f.request(t, ctx)
	token := replayUUID(t, ctx, f.pool)
	_, err := f.pool.Exec(ctx, `INSERT INTO notification_replay_attempts(application_id,request_id,log_source_id,original_partition,original_offset,notification_id,origin_epoch,source_epoch,claim_token,created_at,lease_expires_at,outcome)
 VALUES($1,$2,$3,0,$4,$5,$6,$6,$7,clock_timestamp()-interval '31 seconds',clock_timestamp()-interval '1 second','started')`, f.app, request, f.source, offset, f.campaign, f.epoch, token)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `UPDATE notification_replay_controls SET active_application_id=$1,active_request_id=$2,active_claim_token=$3 WHERE log_source_id=$4 AND original_offset=$5`, f.app, request, token, f.source, offset)
	require.NoError(t, err)
}

func TestReplayRecoveryPreservesPartialProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newReplayFixture(t, ctx)
	seedExpiredReplayClaim(t, ctx, f, 1)
	seedExpiredReplayClaim(t, ctx, f, 2)
	_, err := f.pool.Exec(ctx, `UPDATE staff_users SET is_active=false;
 ALTER TABLE audit_logs ADD CONSTRAINT reject_second_recovery CHECK (action<>'NOTIFICATION_REPLAY_ABANDONED' OR after_json->>'offset'<>'2') NOT VALID`)
	require.NoError(t, err)
	recovery, err := dispatch.NewReplayRecovery(f.pool)
	require.NoError(t, err)
	report, err := recovery.Tick(ctx, 10)
	require.Error(t, err)
	require.Equal(t, 1, report.Abandoned)
	require.Equal(t, 2, report.Visited)
	var state string
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT outcome FROM notification_replay_attempts WHERE original_offset=2").Scan(&state))
	require.Equal(t, "started", state, "audit failure rolls back abandonment and release")
	var n int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_replay_controls WHERE active_claim_token IS NOT NULL").Scan(&n))
	require.Equal(t, 1, n)
	_, err = f.pool.Exec(ctx, "ALTER TABLE audit_logs DROP CONSTRAINT reject_second_recovery")
	require.NoError(t, err)
	report, err = recovery.Tick(ctx, 10)
	require.NoError(t, err, "cleanup needs no live operator credential, active project, source or staff")
	require.Equal(t, 1, report.Abandoned)
	report, err = recovery.Tick(ctx, 10)
	require.NoError(t, err)
	require.Zero(t, report.Visited)
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_replay_controls WHERE active_claim_token IS NOT NULL OR resolved_outcome IS NOT NULL").Scan(&n))
	require.Zero(t, n, "cleanup releases claims without claiming publication or resolution")
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE action='NOTIFICATION_REPLAY_ABANDONED' AND after_json->>'executor'='expired_claim_recovery'").Scan(&n))
	require.Equal(t, 2, n)
}
