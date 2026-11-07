//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/fcm"
)

func TestDeliveryPollerRetainsUnchargedQuotaDeferralAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx, "fixture-quota")
	executionSQL(t, ctx, f, `UPDATE notifications SET payload='{"fixture":"integration"}'::jsonb`)
	var quotaDevice pgtype.UUID
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT d.id FROM notification_delivery_intents d
		JOIN mobile_installations i ON i.id=d.installation_id WHERE i.public_id=$1`, f.enrollment.State.PublicID).Scan(&quotaDevice))
	execution := executionService(t, f.pool)
	sender := submissionSender(t, execution, submissionClient(t, f, submissionAccess))
	lease, err := execution.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	result, err := sender.Send(ctx, lease, quotaDevice)
	require.NoError(t, err)
	out, ok := result.Outcome()
	require.True(t, ok)
	require.Equal(t, fcm.Quota, out.Reason)
	_, err = execution.RecordResult(ctx, result)
	require.NoError(t, err)
	require.NoError(t, execution.Release(ctx, lease))

	policy := dispatch.DeliveryPollPolicy{ApplicationID: f.project, CampaignPageSize: 10, DevicePageSize: 10, Concurrency: 1, RenewInterval: time.Second}
	poller, err := dispatch.NewDeliveryPoller(execution, sender, policy)
	require.NoError(t, err)
	report, err := poller.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, report.Deferred)
	require.Zero(t, report.Recorded)
	var pending bool
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status='PENDING' AND delivery_version=0 AND attempt_id IS NULL
		AND attempt_count=0 AND next_attempt_at>=clock_timestamp()+interval '45 seconds'
		FROM notification_delivery_intents WHERE notification_id=$1 AND id<>$2`, f.campaign, quotaDevice).Scan(&pending))
	require.True(t, pending)
	var events int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM notification_update_outbox WHERE notification_id=$1`, f.campaign).Scan(&events))
	require.Zero(t, events)

	fresh := executionService(t, f.pool)
	restarted, err := dispatch.NewDeliveryPoller(fresh, submissionSender(t, fresh, submissionClient(t, f, submissionAccess)), policy)
	require.NoError(t, err)
	report, err = restarted.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, report.Visited)
	require.Equal(t, 1, executionAttempts(t, ctx, f))
}
