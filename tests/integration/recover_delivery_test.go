//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/installation"
)

func TestRecoverDeliveryKeepsLiveSubmissionResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newResultFixture(t, ctx, "execution-address-0")
	executionSQL(t, ctx, f.executionFixture, "UPDATE notifications SET deadline_at=clock_timestamp()-interval '1 second'")

	changed, err := f.service.RecoverDelivery(ctx, f.lease, f.delivery)
	require.NoError(t, err)
	require.False(t, changed)
	status, version, events := f.state(t, ctx)
	require.Equal(t, "PENDING", status)
	require.Zero(t, version)
	require.Zero(t, events)

	_, err = f.service.RecordResult(ctx, f.result)
	require.NoError(t, err)
	changed, err = f.service.RecoverDelivery(ctx, f.lease, f.delivery)
	require.NoError(t, err)
	require.False(t, changed)
	status, version, events = f.state(t, ctx)
	require.Equal(t, "SENT", status)
	require.EqualValues(t, 1, version)
	require.Equal(t, 1, events)

	// The held application can still close the unsent device after expiry.
	executionSQL(t, ctx, f.executionFixture, "UPDATE application_dispatch_policy SET hold_new=true")
	for _, other := range f.deliveries {
		if other == f.delivery {
			continue
		}
		changed, err = f.service.RecoverDelivery(ctx, f.lease, other)
		require.NoError(t, err)
		require.True(t, changed)
		event, _ := (resultFixture{executionFixture: f.executionFixture, delivery: other}).event(t, ctx)
		require.NotNil(t, event.Reason)
		require.Equal(t, "ttl_expired", *event.Reason)
	}
}

func TestRecoverDeliveryFencesAbandonedAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	// A new worker must retain the charge, its identity and its earliest retry time.
	policy := executionPolicy()
	policy.MaxAttempts = 2
	policy.RetryFloor = time.Minute
	first, err := dispatch.NewExecution(f.pool, policy)
	require.NoError(t, err)
	firstLease, err := first.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	id, retryable := f.deliveries[0], f.deliveries[1]
	_, err = first.Reserve(ctx, firstLease, id)
	require.NoError(t, err)
	require.NoError(t, first.Release(ctx, firstLease))
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp() WHERE id=$1", id)
	firstLease, err = first.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	_, err = first.Reserve(ctx, firstLease, id)
	require.NoError(t, err)
	_, err = first.Reserve(ctx, firstLease, retryable)
	require.NoError(t, err)
	var oldAttempt, currentAttempt pgtype.UUID
	var oldFloor, currentFloor time.Time
	const attempt = "SELECT attempt_id,next_attempt_at FROM notification_delivery_intents WHERE id=$1"
	require.NoError(t, f.pool.QueryRow(ctx, attempt, id).Scan(&oldAttempt, &oldFloor))

	changed, err := first.RecoverDelivery(ctx, firstLease, id)
	require.NoError(t, err)
	require.False(t, changed)
	executionSQL(t, ctx, f, "UPDATE notifications SET lease_until=clock_timestamp()-interval '1 second'")
	second, err := dispatch.NewExecution(f.pool, policy)
	require.NoError(t, err)
	secondLease, err := second.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	changed, err = first.RecoverDelivery(ctx, firstLease, id)
	require.Error(t, err)
	require.False(t, changed)

	changed, err = second.RecoverDelivery(ctx, secondLease, id)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, f.pool.QueryRow(ctx, attempt, id).Scan(&currentAttempt, &currentFloor))
	require.Equal(t, oldAttempt, currentAttempt)
	require.Equal(t, oldFloor, currentFloor)
	require.Equal(t, 3, executionAttempts(t, ctx, f))
	event, _ := (resultFixture{executionFixture: f, delivery: id}).event(t, ctx)
	require.Equal(t, "FAILED", event.Status)
	require.NotNil(t, event.Reason)
	require.Equal(t, "retry_budget_exhausted", *event.Reason)
	require.Nil(t, event.ProviderMessageID)
	changed, err = second.RecoverDelivery(ctx, secondLease, retryable)
	require.NoError(t, err)
	require.False(t, changed)
	_, err = second.Reserve(ctx, secondLease, retryable)
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp() WHERE id=$1", retryable)
	_, err = second.Reserve(ctx, secondLease, retryable)
	require.NoError(t, err)
	require.Equal(t, 4, executionAttempts(t, ctx, f))
}

func TestRecoverDeliveryRetiresObsoleteAudience(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	s := executionService(t, f.pool)
	lease, err := s.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	var original pgtype.UUID
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT d.id FROM notification_delivery_intents d
		JOIN mobile_installations i ON i.id=d.installation_id WHERE i.public_id=$1`,
		f.enrollment.State.PublicID).Scan(&original))
	_, err = f.lifecycle.Apply(ctx, installation.Principal{ApplicationID: f.app, UserID: f.user}, installation.Change{
		InstallationID: f.enrollment.State.PublicID, Proof: f.enrollment.Proof,
		ExpectedVersion: f.enrollment.State.Version, Operation: installation.Refresh,
		Route:   installation.Route{Namespace: "execution-fixture", Key: "route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken},
		Address: "replacement-recovery-address",
	})
	require.NoError(t, err)
	changed, err := s.RecoverDelivery(ctx, lease, original)
	require.NoError(t, err)
	require.True(t, changed)
	event, _ := (resultFixture{executionFixture: f, delivery: original}).event(t, ctx)
	require.NotNil(t, event.Reason)
	require.Equal(t, "token_changed", *event.Reason)
	other := f.deliveries[0]
	if other == original {
		other = f.deliveries[1]
	}
	changed, err = s.RecoverDelivery(ctx, lease, other)
	require.NoError(t, err)
	require.False(t, changed)
	executionSQL(t, ctx, f, "UPDATE users SET status='INACTIVE' WHERE id=$1", f.user)
	changed, err = s.RecoverDelivery(ctx, lease, other)
	require.NoError(t, err)
	require.True(t, changed)
	event, _ = (resultFixture{executionFixture: f, delivery: other}).event(t, ctx)
	require.NotNil(t, event.Reason)
	require.Equal(t, "device_inactive", *event.Reason)
	var current int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM installation_bindings WHERE retired_at IS NULL AND owner_user_id=$1", f.user).Scan(&current))
	require.Equal(t, 2, current)
	require.Zero(t, executionAttempts(t, ctx, f))
}

func TestRecoverDeliveryDeadlineAndOutboxCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	db := &resultCommitDB{Pool: f.pool}
	s := executionService(t, db)
	lease, err := s.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	id := f.deliveries[0]
	stored := resultFixture{executionFixture: f, delivery: id}
	// The saved retry floor is exactly the campaign deadline, so no legal send remains.
	executionSQL(t, ctx, f, `UPDATE notification_delivery_intents d SET next_attempt_at=n.deadline_at
		FROM notifications n WHERE d.notification_id=n.id AND d.id=$1`, id)
	executionSQL(t, ctx, f, `ALTER TABLE notification_update_outbox
		ADD CONSTRAINT reject_recovery_event CHECK (false)`)
	changed, err := s.RecoverDelivery(ctx, lease, id)
	require.Error(t, err)
	require.False(t, changed)
	status, version, events := stored.state(t, ctx)
	require.Equal(t, "PENDING", status)
	require.Zero(t, version)
	require.Zero(t, events)
	executionSQL(t, ctx, f, "ALTER TABLE notification_update_outbox DROP CONSTRAINT reject_recovery_event")

	db.uncertain.Store(true)
	changed, err = s.RecoverDelivery(ctx, lease, id)
	require.Error(t, err)
	require.False(t, changed)
	event, payload := stored.event(t, ctx)
	require.Equal(t, "FAILED", event.Status)
	require.NotNil(t, event.Reason)
	require.Equal(t, "ttl_expired", *event.Reason)
	require.NotNil(t, event.FailedAt)
	require.Nil(t, event.ProviderAcceptedAt)
	require.Equal(t, event.OccurredAt, *event.FailedAt)
	changed, err = s.RecoverDelivery(ctx, lease, id)
	require.NoError(t, err)
	require.False(t, changed)
	_, replayed := stored.event(t, ctx)
	require.Equal(t, payload, replayed)
	status, version, events = stored.state(t, ctx)
	require.Equal(t, "FAILED", status)
	require.EqualValues(t, 1, version)
	require.Equal(t, 1, events)
	require.Zero(t, executionAttempts(t, ctx, f))
}

func TestAbandonedClaimsPersistIncreasingRetryWindow(t *testing.T) {
	ctx := t.Context()
	f := newExecutionFixture(t, ctx)
	s := executionService(t, f.pool)
	device := f.deliveries[0]
	for charge := 1; charge <= 3; charge++ {
		lease, err := s.Acquire(ctx, f.project, f.campaign)
		require.NoError(t, err)
		_, err = s.Reserve(ctx, lease, device)
		require.NoError(t, err)
		var attempts int
		var delay float64
		require.NoError(t, f.pool.QueryRow(ctx, `SELECT attempt_count, extract(epoch FROM next_attempt_at - last_attempt_at)::float8
			FROM notification_delivery_intents WHERE id=$1`, device).Scan(&attempts, &delay))
		require.Equal(t, charge, attempts)
		floors := [...]float64{10, 10, 20}
		require.GreaterOrEqual(t, delay, floors[charge-1])
		require.LessOrEqual(t, delay, float64(int64(10)<<(charge-1)))
		// Simulate an expired owner and elapsed retry window without provider I/O.
		if charge < 3 {
			executionSQL(t, ctx, f, "UPDATE notifications SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", f.campaign)
			executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp() WHERE id=$1", device)
		}
	}
}
