//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/features/applications"
)

func TestScheduledDiscoverySkipsOldLineageAndKeepsExpiry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	runtime, err := f.q.LockNotificationRuntime(ctx)
	require.NoError(t, err)
	seed := func(origin, source pgtype.UUID, scheduled, deadline time.Time) int64 {
		var id int64
		require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notifications(application_id,title,body,created_by,status,scheduled_at,deadline_at,origin_epoch,source_epoch)
   VALUES($1,'Scheduled fixture','Fixture',$2,'SCHEDULED',$3,$4,$5,$6) RETURNING id`, f.app, f.staff, scheduled, deadline, origin, source).Scan(&id))
		return id
	}
	now := time.Now().UTC()
	seed(replayUUID(t, ctx, f.pool), runtime.SourceEpoch, now.Add(-3*time.Hour), now.Add(time.Hour))
	seed(runtime.OriginEpoch, replayUUID(t, ctx, f.pool), now.Add(-2*time.Hour), now.Add(time.Hour))
	live := seed(runtime.OriginEpoch, runtime.SourceEpoch, now.Add(-time.Hour), now.Add(time.Hour))
	expired := seed(runtime.OriginEpoch, runtime.SourceEpoch, now.Add(-30*time.Minute), now.Add(-10*time.Minute))
	rows, err := f.q.ListDueScopedSchedules(ctx, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, live, rows[0].ID, "stale lineage must not consume the first discovery page")
	_, err = f.pool.Exec(ctx, "UPDATE notification_runtime SET migration_hold=true")
	require.NoError(t, err)
	rows, err = f.q.ListDueScopedSchedules(ctx, 100)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, expired, rows[1].ID, "expired accepted work still needs terminalization during a migration hold")
	_, err = f.pool.Exec(ctx, "UPDATE notification_runtime SET sealed=true")
	require.NoError(t, err)
	rows, err = f.q.ListDueScopedSchedules(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, rows)
}

type secondPromotionFailure struct {
	applications.Transactor
	calls int
}

func (db *secondPromotionFailure) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	db.calls++
	if db.calls == 2 {
		return nil, errors.New("fixture second transaction failure")
	}
	return db.Transactor.BeginTx(ctx, options)
}

func TestScheduleSweepRetainsPartialProgressAndRetriesOnlyPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	_, err := f.pool.Exec(ctx, "UPDATE notification_runtime SET migration_hold=true")
	require.NoError(t, err)
	var ids []int64
	for i := range 3 {
		var id int64
		require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notifications(application_id,title,body,created_by,status,scheduled_at,deadline_at,origin_epoch,source_epoch)
   SELECT $1,'Sweep','Fixture',$2,'SCHEDULED',clock_timestamp()-($3::integer*interval '1 minute'),
   CASE WHEN $3::integer=1 THEN clock_timestamp()-interval '10 seconds' ELSE clock_timestamp()+interval '1 hour' END,origin_epoch,source_epoch FROM notification_runtime RETURNING id`, f.app, f.staff, 3-i).Scan(&id))
		ids = append(ids, id)
	}
	failure := &secondPromotionFailure{Transactor: f.pool}
	report, err := applications.NewService(f.q, failure).PromoteDueSchedules(ctx, 100)
	require.Error(t, err)
	require.Equal(t, applications.PromotionReport{Visited: 2, Changed: 1}, report)
	var queued, outboxes int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notifications WHERE status='QUEUED'").Scan(&queued))
	require.Equal(t, 1, queued)
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_send_outbox").Scan(&outboxes))
	require.Equal(t, 1, outboxes)
	report, err = applications.NewService(f.q, f.pool).PromoteDueSchedules(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, applications.PromotionReport{Visited: 2, Changed: 2}, report)
	var expired string
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT status FROM notifications WHERE id=$1", ids[2]).Scan(&expired))
	require.Equal(t, "FAILED", expired)
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_send_outbox").Scan(&outboxes))
	require.Equal(t, 2, outboxes, "expiry cannot publish; the first committed promotion is not repeated")
	report, err = applications.NewService(f.q, f.pool).PromoteDueSchedules(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, applications.PromotionReport{}, report)
}
