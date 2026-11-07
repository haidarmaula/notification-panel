//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
)

func TestFinalizeCampaignWaitsForLiveAttemptAfterReceipt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	service := executionService(t, f.pool)
	original, err := service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	done, err := service.FinalizeCampaign(ctx, original)
	require.NoError(t, err)
	require.False(t, done)
	_, err = service.Reserve(ctx, original, f.deliveries[0])
	require.NoError(t, err)
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET status='OPENED' WHERE id=$1", f.deliveries[0])
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET status='FAILED' WHERE id=$1", f.deliveries[1])
	done, err = service.FinalizeCampaign(ctx, original)
	require.NoError(t, err)
	require.False(t, done, "the current provider result can still enrich the early receipt")

	executionSQL(t, ctx, f, "UPDATE notifications SET lease_until=clock_timestamp()-interval '1 second'")
	replacement, err := service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	done, err = service.FinalizeCampaign(ctx, original)
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
	require.False(t, done)
	executionSQL(t, ctx, f, "UPDATE application_dispatch_policy SET hold_new=true; UPDATE applications SET status='HELD'")
	done, err = service.FinalizeCampaign(ctx, replacement)
	require.NoError(t, err)
	require.True(t, done)
	var correct bool
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status='COMPLETED' AND completed_at IS NOT NULL
		AND lifecycle_reason IS NULL AND lease_owner IS NULL AND lease_until IS NULL AND lease_generation=2
		FROM notifications WHERE id=$1`, f.campaign).Scan(&correct))
	require.True(t, correct)
	require.Equal(t, 1, executionAttempts(t, ctx, f))
}

func TestFinalizeCampaignAllFailedAfterLostAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET status='FAILED', observed_opened_at=clock_timestamp()")
	service := executionService(t, &replayNthCommitFailure{Pool: f.pool})
	lease, err := service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	done, err := service.FinalizeCampaign(ctx, lease)
	require.ErrorContains(t, err, "acknowledgement lost")
	require.False(t, done)
	var completedAt time.Time
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT completed_at FROM notifications WHERE id=$1
		AND status='FAILED' AND lifecycle_reason='all_deliveries_failed' AND lease_owner IS NULL`, f.campaign).Scan(&completedAt))
	done, err = service.FinalizeCampaign(ctx, lease)
	require.Error(t, err)
	require.False(t, done)
	var same bool
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT completed_at=$2 FROM notifications WHERE id=$1", f.campaign, completedAt).Scan(&same))
	require.True(t, same)
}

func TestFinalizeCampaignRequiresCompleteAudience(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		f := newReplayRegistrationFixture(t, ctx)
		event := f.acceptedEvent(t, ctx)
		preparer, err := dispatch.NewPreparer(f.pool)
		require.NoError(t, err)
		prepared, err := preparer.Prepare(ctx, event)
		require.NoError(t, err)
		require.True(t, prepared.SnapshotComplete)
		_, err = f.pool.Exec(ctx, "UPDATE notifications SET status='PROCESSING', completed_at=NULL WHERE id=$1", f.campaign)
		require.NoError(t, err)
		service := executionService(t, f.pool)
		lease, err := service.Acquire(ctx, f.project, f.campaign)
		require.NoError(t, err)
		done, err := service.FinalizeCampaign(ctx, lease)
		require.NoError(t, err)
		require.True(t, done)
		var status string
		require.NoError(t, f.pool.QueryRow(ctx, "SELECT status FROM notifications WHERE id=$1", f.campaign).Scan(&status))
		require.Equal(t, "COMPLETED", status)
	})
	t.Run("missing deliveries", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		f := newExecutionFixture(t, ctx)
		service := executionService(t, f.pool)
		lease, err := service.Acquire(ctx, f.project, f.campaign)
		require.NoError(t, err)
		executionSQL(t, ctx, f, "DELETE FROM notification_delivery_intents")
		done, err := service.FinalizeCampaign(ctx, lease)
		require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
		require.False(t, done)
		var status string
		require.NoError(t, f.pool.QueryRow(ctx, "SELECT status FROM notifications WHERE id=$1", f.campaign).Scan(&status))
		require.Equal(t, "PROCESSING", status)
	})
}
