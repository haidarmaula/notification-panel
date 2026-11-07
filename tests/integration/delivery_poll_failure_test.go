//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
)

func TestDeliveryPollerContinuesPastFailedRetirement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		blocked  int
		pageSize int32
	}{
		{name: "failure at page head", blocked: 0, pageSize: 1},
		{name: "healthy prefix on same page", blocked: 1, pageSize: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			f := readySubmission(t, ctx)
			blocked, healthy := f.deliveries[tc.blocked], f.deliveries[1-tc.blocked]
			executionSQL(t, ctx, f, `UPDATE notification_delivery_intents d SET next_attempt_at=n.deadline_at
				FROM notifications n WHERE d.notification_id=n.id AND d.id=$1`, blocked)
			executionSQL(t, ctx, f, `ALTER TABLE notification_update_outbox ADD CONSTRAINT reject_fixture_delivery
				CHECK (delivery_id <> '`+blocked.String()+`'::uuid)`)
			execution := executionService(t, f.pool)
			sender := submissionSender(t, execution, submissionClient(t, f, submissionAccess))
			policy := dispatch.DeliveryPollPolicy{ApplicationID: f.project, CampaignPageSize: 1,
				DevicePageSize: tc.pageSize, Concurrency: 1, RenewInterval: time.Second}
			poller, err := dispatch.NewDeliveryPoller(execution, sender, policy)
			require.NoError(t, err)

			var failures, recorded int
			for range 8 {
				report, err := poller.Tick(ctx)
				recorded += report.Recorded
				if err != nil {
					var dbErr *pgconn.PgError
					require.ErrorAs(t, err, &dbErr)
					require.Equal(t, "reject_fixture_delivery", dbErr.ConstraintName)
					failures++
				}
			}
			require.Positive(t, failures, "the worker should report the rejected outbox insert")
			require.Equal(t, 1, recorded)
			var blockedPending, accepted bool
			var updates int
			require.NoError(t, f.pool.QueryRow(ctx, `SELECT
				(SELECT status='PENDING' AND attempt_count=0 AND delivery_version=0 FROM notification_delivery_intents WHERE id=$1),
				(SELECT status='SENT' AND provider_message_id IS NOT NULL AND attempt_count=1 AND delivery_version=1
				 FROM notification_delivery_intents WHERE id=$2),
				(SELECT count(*) FROM notification_update_outbox WHERE delivery_id IN ($1,$2))`, blocked, healthy).
				Scan(&blockedPending, &accepted, &updates))
			require.True(t, blockedPending)
			require.True(t, accepted)
			require.Equal(t, 1, updates)

			executionSQL(t, ctx, f, "ALTER TABLE notification_update_outbox DROP CONSTRAINT reject_fixture_delivery")
			var finalized bool
			for range 4 {
				report, err := poller.Tick(ctx)
				require.NoError(t, err)
				finalized = finalized || report.Finalized == 1
				require.Zero(t, report.Recorded)
			}
			require.True(t, finalized, "the failed retirement should resume after the outbox is repaired")
			event, _ := (resultFixture{executionFixture: f, delivery: blocked}).event(t, ctx)
			require.Equal(t, "FAILED", event.Status)
			require.NotNil(t, event.Reason)
			require.Equal(t, "ttl_expired", *event.Reason)
		})
	}
}
