//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
)

func TestExecutionDiscoverySkipsHeldLineageAndPagesPastLiveCampaign(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	s := executionService(t, f.pool)
	insertCampaign := func() int64 {
		t.Helper()
		var id int64
		require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notifications(
			application_id,title,body,created_by,status,queued_at,deadline_at,origin_epoch,source_epoch)
			SELECT application_id,title,body,created_by,status,queued_at,deadline_at,origin_epoch,source_epoch
			FROM notifications WHERE id=$1 RETURNING id`, f.campaign).Scan(&id))
		return id
	}
	insertCampaign() // Incomplete legacy fan-out.
	wrongMarker := insertCampaign()
	empty := insertCampaign()
	executionSQL(t, ctx, f, `INSERT INTO notification_preparations(
		application_id,notification_id,origin_epoch,source_epoch,resolved_user_count,eligible_user_count,eligible_device_count)
		SELECT application_id,id,origin_epoch,CASE WHEN id=$1 THEN gen_random_uuid() ELSE source_epoch END,0,0,0
		FROM notifications WHERE id IN ($1,$2)`, wrongMarker, empty)

	first, err := s.DiscoverCampaigns(ctx, 0, 1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, f.campaign, first[0].NotificationID)
	second, err := s.DiscoverCampaigns(ctx, first[0].NotificationID, 1)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, empty, second[0].NotificationID)
	end, err := s.DiscoverCampaigns(ctx, empty, 1)
	require.NoError(t, err)
	require.Empty(t, end)

	_, err = s.AcquireDiscovered(ctx, first[0])
	require.NoError(t, err)
	visible, err := s.DiscoverCampaigns(ctx, 0, 10)
	require.NoError(t, err)
	require.Equal(t, second, visible)
	_, err = s.AcquireDiscovered(ctx, first[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)

	// Another worker can finish the campaign between discovery and acquisition.
	executionSQL(t, ctx, f, "UPDATE notifications SET status='COMPLETED' WHERE id=$1", empty)
	_, err = s.AcquireDiscovered(ctx, second[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
	executionSQL(t, ctx, f, "UPDATE notifications SET status='PROCESSING' WHERE id=$1", empty)

	executionSQL(t, ctx, f, "UPDATE notification_runtime SET migration_hold=true")
	visible, err = s.DiscoverCampaigns(ctx, 0, 10)
	require.NoError(t, err)
	require.Empty(t, visible)
	_, err = s.AcquireDiscovered(ctx, second[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
	executionSQL(t, ctx, f, "UPDATE notification_runtime SET migration_hold=false,source_epoch=gen_random_uuid()")
	visible, err = s.DiscoverCampaigns(ctx, 0, 10)
	require.NoError(t, err)
	require.Empty(t, visible)
	_, err = s.AcquireDiscovered(ctx, second[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
}

func TestExecutionRecoveryPageDoesNotChargeCandidatesAndResumesAfterFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	s := executionService(t, f.pool)
	lease, err := s.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	var start pgtype.UUID
	page, err := s.RecoverPage(ctx, lease, start, 1)
	require.NoError(t, err)
	require.Equal(t, f.deliveries[:1], page.Ready)
	require.Equal(t, f.deliveries[0], page.After)
	require.True(t, page.More)
	page, err = s.RecoverPage(ctx, lease, page.After, 1)
	require.NoError(t, err)
	require.Equal(t, f.deliveries[1:], page.Ready)
	require.Equal(t, f.deliveries[1], page.After)
	require.False(t, page.More)
	require.False(t, page.CampaignFinalized)
	require.Zero(t, executionAttempts(t, ctx, f))

	// Only the first retirement commits. Resume after that ID when persistence recovers.
	executionSQL(t, ctx, f, "UPDATE notifications SET deadline_at=clock_timestamp()-interval '1 second'")
	executionSQL(t, ctx, f, `ALTER TABLE notification_update_outbox ADD CONSTRAINT reject_second_event
		CHECK (delivery_id <> '`+f.deliveries[1].String()+`'::uuid)`)
	page, err = s.RecoverPage(ctx, lease, start, 2)
	require.Error(t, err)
	require.Equal(t, 1, page.Retired)
	require.Equal(t, f.deliveries[0], page.After)
	require.False(t, page.CampaignFinalized)
	executionSQL(t, ctx, f, "ALTER TABLE notification_update_outbox DROP CONSTRAINT reject_second_event")
	page, err = s.RecoverPage(ctx, lease, page.After, 2)
	require.NoError(t, err)
	require.True(t, page.CampaignFinalized)
	require.Equal(t, 1, page.Retired)
	require.Empty(t, page.Ready)
	visible, err := s.DiscoverCampaigns(ctx, 0, 1)
	require.NoError(t, err)
	require.Empty(t, visible)
	var status, reason string
	var events int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status,lifecycle_reason,
		(SELECT count(*) FROM notification_update_outbox WHERE notification_id=n.id)
		FROM notifications n WHERE n.id=$1`, f.campaign).Scan(&status, &reason, &events))
	require.Equal(t, "FAILED", status)
	require.Equal(t, "all_deliveries_failed", reason)
	require.Equal(t, 2, events)
	require.Zero(t, executionAttempts(t, ctx, f))
}

func TestExecutionRecoveryLeaseKeepsBookkeepingOnlyAfterHoldLifted(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	s := executionService(t, f.pool)
	executionSQL(t, ctx, f, "UPDATE application_dispatch_policy SET hold_new=true")
	campaigns, err := s.DiscoverCampaigns(ctx, 0, 1)
	require.NoError(t, err)
	require.Empty(t, campaigns)
	executionSQL(t, ctx, f, `UPDATE notification_delivery_intents d SET next_attempt_at=n.deadline_at
		FROM notifications n WHERE d.notification_id=n.id AND d.id=$1`, f.deliveries[0])
	campaigns, err = s.DiscoverCampaigns(ctx, 0, 1)
	require.NoError(t, err)
	require.Len(t, campaigns, 1)
	lease, err := s.AcquireDiscovered(ctx, campaigns[0])
	require.NoError(t, err)
	executionSQL(t, ctx, f, "UPDATE application_dispatch_policy SET hold_new=false")
	reservation, err := s.Reserve(ctx, lease, f.deliveries[1])
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
	require.Nil(t, reservation)
	page, err := s.RecoverPage(ctx, lease, pgtype.UUID{}, 10)
	require.NoError(t, err)
	require.Equal(t, 1, page.Retired)
	require.Empty(t, page.Ready)
	require.False(t, page.CampaignFinalized)
	require.Zero(t, executionAttempts(t, ctx, f))
	require.NoError(t, s.Release(ctx, lease))

	lease, err = s.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	page, err = s.RecoverPage(ctx, lease, pgtype.UUID{}, 10)
	require.NoError(t, err)
	require.Equal(t, f.deliveries[1:], page.Ready)
}
