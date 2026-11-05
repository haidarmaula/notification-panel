//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/installation"
)

type executionFixture struct {
	replayRegistrationFixture
	user       int64
	deliveries []pgtype.UUID
	enrollment installation.Enrollment
	lifecycle  *installation.Lifecycle
}

func newExecutionFixture(t *testing.T, ctx context.Context) executionFixture {
	t.Helper()
	f := executionFixture{replayRegistrationFixture: newReplayRegistrationFixture(t, ctx)}
	event := f.acceptedEvent(t, ctx)
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO users(application_id,external_id) VALUES($1,'execution-user') RETURNING id", f.app).Scan(&f.user))
	protector, err := installation.NewProtector(1, map[int32][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	lifecycle, err := installation.NewLifecycle(f.pool, protector)
	require.NoError(t, err)
	f.lifecycle = lifecycle
	for i := 0; i < 2; i++ {
		var enrollment installation.Enrollment
		enrollment, err = lifecycle.Enroll(ctx, installation.Principal{ApplicationID: f.app, UserID: f.user}, installation.Route{Namespace: "execution-fixture", Key: "route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}, fmt.Sprintf("execution-address-%d", i))
		require.NoError(t, err)
		if i == 0 {
			f.enrollment = enrollment
		}
	}
	preparer, err := dispatch.NewPreparer(f.pool)
	require.NoError(t, err)
	result, err := preparer.Prepare(ctx, event)
	require.NoError(t, err)
	require.True(t, result.SnapshotComplete)
	rows, err := f.pool.Query(ctx, "SELECT id FROM notification_delivery_intents ORDER BY id")
	require.NoError(t, err)
	for rows.Next() {
		var id pgtype.UUID
		require.NoError(t, rows.Scan(&id))
		f.deliveries = append(f.deliveries, id)
	}
	require.NoError(t, rows.Err())
	rows.Close()
	require.Len(t, f.deliveries, 2)
	return f
}
func executionPolicy() dispatch.ExecutionPolicy {
	return dispatch.ExecutionPolicy{LeaseDuration: time.Minute, RetryFloor: 10 * time.Second, MaxAttempts: 3}
}
func executionService(t *testing.T, db dispatch.Transactor) *dispatch.Execution {
	t.Helper()
	s, err := dispatch.NewExecution(db, executionPolicy())
	require.NoError(t, err)
	return s
}
func executionSQL(t *testing.T, ctx context.Context, f executionFixture, query string, args ...any) {
	t.Helper()
	_, err := f.pool.Exec(ctx, query, args...)
	require.NoError(t, err)
}
func executionAttempts(t *testing.T, ctx context.Context, f executionFixture) int {
	t.Helper()
	var count int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT sum(attempt_count) FROM notification_delivery_intents").Scan(&count))
	return count
}

func TestExecutionConcurrentLeaseReservationAndTakeover(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	first, second := executionService(t, f.pool), executionService(t, f.pool)
	type claim struct {
		service *dispatch.Execution
		lease   *dispatch.ExecutionLease
		err     error
	}
	claims := make(chan claim, 2)
	var wg sync.WaitGroup
	for _, service := range []*dispatch.Execution{first, second} {
		wg.Go(func() {
			lease, err := service.Acquire(ctx, f.project, f.campaign)
			claims <- claim{service, lease, err}
		})
	}
	wg.Wait()
	close(claims)
	var owner claim
	denied := 0
	for got := range claims {
		if got.err == nil {
			require.NotNil(t, got.lease)
			owner = got
		} else {
			require.ErrorIs(t, got.err, dispatch.ErrExecutionUnavailable)
			require.Nil(t, got.lease)
			denied++
		}
	}
	require.NotNil(t, owner.lease)
	require.Equal(t, 1, denied)
	require.NoError(t, owner.service.Renew(ctx, owner.lease))
	reservations := make(chan *dispatch.AttemptReservation, 2)
	failures := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			r, err := owner.service.Reserve(ctx, owner.lease, f.deliveries[0])
			reservations <- r
			failures <- err
		})
	}
	wg.Wait()
	close(reservations)
	close(failures)
	granted := 0
	for r := range reservations {
		if r != nil {
			granted++
		}
	}
	require.Equal(t, 1, granted)
	for err := range failures {
		if err != nil {
			require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
		}
	}
	require.Equal(t, 1, executionAttempts(t, ctx, f))
	// Even a due-time alteration cannot recharge an unresolved same-generation claim.
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp()-interval '1 second'")
	r, err := owner.service.Reserve(ctx, owner.lease, f.deliveries[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
	require.Nil(t, r)
	r, err = owner.service.Reserve(ctx, owner.lease, f.deliveries[1])
	require.NoError(t, err)
	require.NotNil(t, r)
	var floor float64
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT extract(epoch FROM next_attempt_at-last_attempt_at)::float8 FROM notification_delivery_intents WHERE id=$1", f.deliveries[1]).Scan(&floor))
	require.GreaterOrEqual(t, floor, 10.0)
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp()+interval '1 minute'; UPDATE notifications SET lease_until=clock_timestamp()-interval '1 second'")
	require.ErrorIs(t, owner.service.Renew(ctx, owner.lease), dispatch.ErrExecutionUnavailable)
	replacement := executionService(t, f.pool)
	// Future retries have no actionable work yet, even though ownership expired.
	lease, err := replacement.Acquire(ctx, f.project, f.campaign)
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
	require.Nil(t, lease)
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1", f.deliveries[0])
	lease, err = replacement.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	require.ErrorIs(t, owner.service.Release(ctx, owner.lease), dispatch.ErrExecutionUnavailable)
	r, err = owner.service.Reserve(ctx, owner.lease, f.deliveries[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
	require.Nil(t, r)
	r, err = replacement.Reserve(ctx, lease, f.deliveries[1])
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
	require.Nil(t, r)
	r, err = replacement.Reserve(ctx, lease, f.deliveries[0])
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, 3, executionAttempts(t, ctx, f))
	require.NoError(t, replacement.Release(ctx, lease))
	require.ErrorIs(t, replacement.Renew(ctx, lease), dispatch.ErrExecutionUnavailable)
	var generation int64
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT lease_generation FROM notifications WHERE id=$1", f.campaign).Scan(&generation))
	require.EqualValues(t, 2, generation)
	// Exhausted/expired pending work must remain ownable by a no-send finalizer.
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET attempt_count=3,next_attempt_at=clock_timestamp()+interval '1 hour'")
	lease, err = replacement.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	r, err = replacement.Reserve(ctx, lease, f.deliveries[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
	require.Nil(t, r)
	require.NoError(t, replacement.Release(ctx, lease))
	policy := executionPolicy()
	policy.MaxAttempts = 4
	replacement, err = dispatch.NewExecution(f.pool, policy)
	require.NoError(t, err)
	executionSQL(t, ctx, f, "UPDATE notifications SET deadline_at=clock_timestamp()-interval '1 second'")
	lease, err = replacement.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp()-interval '1 second'")
	r, err = replacement.Reserve(ctx, lease, f.deliveries[1])
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
	require.Nil(t, r)
}

func TestExecutionHoldsAndCurrentOwnershipDenyWithoutCharge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	service := executionService(t, f.pool)
	lease, err := service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	var source, origin pgtype.UUID
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT origin_epoch,source_epoch FROM notification_runtime").Scan(&origin, &source))
	for _, tt := range []struct{ name, change, restore string }{
		{"sealed", "UPDATE notification_runtime SET sealed=true", "UPDATE notification_runtime SET sealed=false"},
		{"migration hold", "UPDATE notification_runtime SET migration_hold=true", "UPDATE notification_runtime SET migration_hold=false"},
		{"app held", "UPDATE applications SET status='HELD'", "UPDATE applications SET status='ACTIVE'"},
		{"dispatch held", "UPDATE application_dispatch_policy SET hold_new=true", "UPDATE application_dispatch_policy SET hold_new=false"},
		{"coverage revoked", "UPDATE application_dispatch_policy SET coverage_approved=false", "UPDATE application_dispatch_policy SET coverage_approved=true"},
		{"inactive owner", "UPDATE users SET status='INACTIVE'", "UPDATE users SET status='ACTIVE'"},
		{"terminal delivery", "UPDATE notification_delivery_intents SET status='SENT'", "UPDATE notification_delivery_intents SET status='PENDING'"},
		{"future retry", "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp()+interval '1 hour'", "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp()-interval '1 second'"},
		{"terminal campaign", "UPDATE notifications SET status='FAILED'", "UPDATE notifications SET status='PROCESSING'"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			executionSQL(t, ctx, f, tt.change)
			defer executionSQL(t, ctx, f, tt.restore)
			r, err := service.Reserve(ctx, lease, f.deliveries[0])
			require.Error(t, err)
			require.Nil(t, r)
			require.Zero(t, executionAttempts(t, ctx, f))
		})
	}
	executionSQL(t, ctx, f, "UPDATE notification_runtime SET source_epoch=gen_random_uuid()")
	r, err := service.Reserve(ctx, lease, f.deliveries[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
	require.Nil(t, r)
	executionSQL(t, ctx, f, "UPDATE notification_runtime SET source_epoch=$1", source)
	executionSQL(t, ctx, f, "UPDATE notification_runtime SET origin_epoch=gen_random_uuid()")
	require.ErrorIs(t, service.Renew(ctx, lease), dispatch.ErrExecutionHeld)
	executionSQL(t, ctx, f, "UPDATE notification_runtime SET origin_epoch=$1", origin)
	// Another application cannot use a known campaign or delivery identifier.
	var other pgtype.UUID
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO applications(key,name) VALUES('execution-other','Other') RETURNING public_id").Scan(&other))
	executionSQL(t, ctx, f, "UPDATE applications SET status='ACTIVE' WHERE public_id=$1", other)
	executionSQL(t, ctx, f, "INSERT INTO application_dispatch_policy(application_id,hold_new,coverage_approved,push_ttl_seconds,key_ttl_seconds) SELECT id,false,true,300,300 FROM applications WHERE public_id=$1", other)
	lost, err := service.Acquire(ctx, other, f.campaign)
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
	require.Nil(t, lost)
	r, err = executionService(t, f.pool).Reserve(ctx, lease, f.deliveries[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionInput)
	require.Nil(t, r)
	require.Zero(t, executionAttempts(t, ctx, f))
	r, err = service.Reserve(ctx, lease, f.deliveries[0])
	require.NoError(t, err)
	require.NotNil(t, r)
}

func TestExecutionUnknownCommitAndRollbackGiveNoProof(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	service := executionService(t, replayUncertainCommit{f.pool})
	lease, err := service.Acquire(ctx, f.project, f.campaign)
	require.Error(t, err)
	require.Nil(t, lease)
	good := executionService(t, f.pool)
	lease, err = good.Acquire(ctx, f.project, f.campaign)
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
	require.Nil(t, lease)
	executionSQL(t, ctx, f, "UPDATE notifications SET lease_until=clock_timestamp()-interval '1 second'")
	service = executionService(t, &replayNthCommitFailure{Pool: f.pool})
	lease, err = service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	r, err := service.Reserve(ctx, lease, f.deliveries[0])
	require.Error(t, err)
	require.Nil(t, r)
	require.Equal(t, 1, executionAttempts(t, ctx, f))
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp()-interval '1 second'")
	r, err = service.Reserve(ctx, lease, f.deliveries[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
	require.Nil(t, r)
	// A database rejection before commit rolls back its prospective charge.
	executionSQL(t, ctx, f, `ALTER TABLE notification_delivery_intents ADD CONSTRAINT fixture_reject_attempt CHECK(attempt_count=0 OR id<> '`+f.deliveries[1].String()+`'::uuid)`)
	r, err = service.Reserve(ctx, lease, f.deliveries[1])
	require.Error(t, err)
	require.Nil(t, r)
	require.Equal(t, 1, executionAttempts(t, ctx, f))
	executionSQL(t, ctx, f, "ALTER TABLE notification_delivery_intents DROP CONSTRAINT fixture_reject_attempt")
	canceled, stop := context.WithCancel(ctx)
	stop()
	r, err = service.Reserve(canceled, lease, f.deliveries[1])
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, r)
	r, err = service.Reserve(ctx, lease, f.deliveries[1])
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Eventually(t, func() bool { return f.pool.Stat().AcquiredConns() == 0 }, time.Second, 10*time.Millisecond)
}

type executionFrozenClock struct{ *pgxpool.Pool }

func (db executionFrozenClock) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	tx, err := db.Pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SELECT now()"); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	time.Sleep(100 * time.Millisecond)
	return tx, nil
}
func TestExecutionLeaseUsesGuardTimeInsteadOfTransactionStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	service := executionService(t, executionFrozenClock{f.pool})
	lease, err := service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	executionSQL(t, ctx, f, "UPDATE notifications SET lease_until=clock_timestamp()+interval '50 milliseconds'")
	require.ErrorIs(t, service.Renew(ctx, lease), dispatch.ErrExecutionUnavailable)
	executionSQL(t, ctx, f, "UPDATE notifications SET lease_until=clock_timestamp()+interval '50 milliseconds'")
	r, err := service.Reserve(ctx, lease, f.deliveries[0])
	require.True(t, errors.Is(err, dispatch.ErrExecutionUnavailable))
	require.Nil(t, r)
	require.Zero(t, executionAttempts(t, ctx, f))
}

func TestExecutionRejectsChangedBindingAndOwner(t *testing.T) {
	for _, operation := range []installation.Transition{installation.Refresh, installation.Logout, installation.SwitchOwner} {
		t.Run(string(operation), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f := newExecutionFixture(t, ctx)
			enrollment := f.enrollment
			var delivery pgtype.UUID
			require.NoError(t, f.pool.QueryRow(ctx, `SELECT d.id FROM notification_delivery_intents d JOIN mobile_installations i ON i.id=d.installation_id WHERE i.public_id=$1`, enrollment.State.PublicID).Scan(&delivery))
			service := executionService(t, f.pool)
			lease, err := service.Acquire(ctx, f.project, f.campaign)
			require.NoError(t, err)
			actor := installation.Principal{ApplicationID: f.app, UserID: f.user}
			if operation == installation.SwitchOwner {
				require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO users(application_id,external_id) VALUES($1,'replacement-owner') RETURNING id", f.app).Scan(&actor.UserID))
			}
			_, err = f.lifecycle.Apply(ctx, actor, installation.Change{InstallationID: enrollment.State.PublicID, Proof: enrollment.Proof, ExpectedVersion: enrollment.State.Version, Operation: operation, Route: installation.Route{Namespace: "execution-fixture", Key: "route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}, Address: "execution-refreshed-address"})
			require.NoError(t, err)
			reservation, err := service.Reserve(ctx, lease, delivery)
			require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
			require.Nil(t, reservation)
			require.Zero(t, executionAttempts(t, ctx, f))
			// An unrelated installation of the original owner remains eligible.
			other := f.deliveries[0]
			if other == delivery {
				other = f.deliveries[1]
			}
			reservation, err = service.Reserve(ctx, lease, other)
			require.NoError(t, err)
			require.NotNil(t, reservation)
		})
	}
}

func TestExecutionDownRetainsReleasedGenerationAndCharges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newExecutionFixture(t, ctx)
	service := executionService(t, f.pool)
	lease, err := service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	require.NoError(t, service.Release(ctx, lease))
	data, err := os.ReadFile("../../db/migrations/000028_create_execution_claims.down.sql")
	require.NoError(t, err)
	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	down := strings.ReplaceAll(string(data), "public.", pgx.Identifier{schema}.Sanitize()+".")
	_, err = f.pool.Exec(ctx, down)
	require.ErrorContains(t, err, "cannot remove retained execution evidence")
	// Refusal leaves schema and generation usable; it cannot reset ownership.
	lease, err = service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	reservation, err := service.Reserve(ctx, lease, f.deliveries[0])
	require.NoError(t, err)
	require.NotNil(t, reservation)
	_, err = f.pool.Exec(ctx, down)
	require.ErrorContains(t, err, "cannot remove retained execution evidence")
	require.Equal(t, 1, executionAttempts(t, ctx, f))
}
