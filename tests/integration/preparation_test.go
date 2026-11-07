//go:build integration

package integration

import (
	"bytes"
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
	"hello/internal/dispatch"
	"hello/internal/installation"
	"hello/internal/kafka"
)

func TestAtomicPreparation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	pool := acceptanceFixture(t, ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	count := func(sql string, args ...any) int64 {
		t.Helper()
		var n int64
		require.NoError(t, pool.QueryRow(ctx, sql, args...).Scan(&n))
		return n
	}
	exec("ALTER TABLE applications DROP CONSTRAINT applications_status_check")
	appID := count("INSERT INTO applications(key,name,status) VALUES('prep-a','Preparation A','ACTIVE') RETURNING id")
	otherApp := count("INSERT INTO applications(key,name,status) VALUES('prep-b','Preparation B','ACTIVE') RETURNING id")
	staffID := count("INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Preparation','prep@fixture.invalid','fixture' FROM roles WHERE name='ADMIN' RETURNING id")
	active := count("INSERT INTO users(application_id,external_id) VALUES($1,'prep-active') RETURNING id", appID)
	inactive := count("INSERT INTO users(application_id,external_id,status) VALUES($1,'prep-inactive','INACTIVE') RETURNING id", appID)
	otherUser := count("INSERT INTO users(application_id,external_id) VALUES($1,'prep-other') RETURNING id", otherApp)
	protector, err := installation.NewProtector(1, map[int32][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	lifecycle, err := installation.NewLifecycle(pool, protector)
	require.NoError(t, err)
	enroll := func(app, user int64, address string) installation.Enrollment {
		t.Helper()
		enrollment, err := lifecycle.Enroll(ctx, installation.Principal{ApplicationID: app, UserID: user}, installation.Route{Namespace: "preparation-fixture", Key: "route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}, address)
		require.NoError(t, err)
		approveFixtureBindings(t, ctx, pool, app)
		return enrollment
	}
	firstBinding := enroll(appID, active, "prep-address-1")
	enroll(appID, active, "prep-address-2")
	enroll(otherApp, otherUser, "prep-other-address")
	seed := func(userID int64) kafka.ScopedSendRequested {
		t.Helper()
		id := count("INSERT INTO notifications(application_id,title,body,created_by,status,queued_at,deadline_at,origin_epoch,source_epoch) SELECT $1,'Prepare','Body',$2,'QUEUED',clock_timestamp(),clock_timestamp()+interval '1 hour',origin_epoch,source_epoch FROM notification_runtime RETURNING id", appID, staffID)
		if userID == 0 {
			exec("INSERT INTO notification_targets(application_id,notification_id,target_type) VALUES($1,$2,'BROADCAST')", appID, id)
		} else {
			exec("INSERT INTO notification_targets(application_id,notification_id,target_type,user_id) VALUES($1,$2,'INDIVIDUAL',$3)", appID, id, userID)
		}
		var e kafka.ScopedSendRequested
		e.SchemaVersion, e.NotificationID = 1, id
		require.NoError(t, pool.QueryRow(ctx, "SELECT a.public_id::text,n.queued_at,n.deadline_at,n.origin_epoch::text,n.source_epoch::text FROM notifications n JOIN applications a ON a.id=n.application_id WHERE n.id=$1", id).Scan(&e.ApplicationID, &e.RequestedAt, &e.DeadlineAt, &e.OriginEpoch, &e.SourceEpoch))
		return e
	}
	preparer, err := dispatch.NewPreparer(pool)
	require.NoError(t, err)
	event := seed(0)
	_, err = preparer.Prepare(ctx, event)
	require.ErrorIs(t, err, dispatch.ErrAdmissionHeld)
	exec("UPDATE notification_runtime SET sealed=false")
	_, err = preparer.Prepare(ctx, event)
	require.ErrorIs(t, err, dispatch.ErrAdmissionHeld)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notifications WHERE id=$1 AND preparation_hold_reason='migration_hold' AND preparation_next_attempt_at>clock_timestamp()", event.NotificationID))
	require.Zero(t, count("SELECT count(*) FROM notification_delivery_intents"))
	exec("UPDATE notification_runtime SET migration_hold=false")
	exec("INSERT INTO application_dispatch_policy(application_id,hold_new,coverage_approved,push_ttl_seconds,key_ttl_seconds) VALUES($1,false,true,3600,86400)", appID)
	// Complete-marker failure must roll back the already inserted device set.
	exec("ALTER TABLE notification_preparations ADD CONSTRAINT injected_preparation_failure CHECK (notification_id<>" + strconv.FormatInt(event.NotificationID, 10) + ")")
	_, err = preparer.Prepare(ctx, event)
	require.Error(t, err)
	require.Zero(t, count("SELECT count(*) FROM notification_delivery_intents"))
	require.Zero(t, count("SELECT count(*) FROM notification_preparations"))
	require.EqualValues(t, 1, count("SELECT count(*) FROM notifications WHERE id=$1 AND status='QUEUED'", event.NotificationID))
	exec("ALTER TABLE notification_preparations DROP CONSTRAINT injected_preparation_failure")
	// Overlapping target paths still produce one intent per binding.
	segment := count("INSERT INTO segments(application_id,name,created_by) VALUES($1,'Prep segment',$2) RETURNING id", appID, staffID)
	exec("INSERT INTO segment_members(application_id,segment_id,user_id) VALUES($1,$2,$3)", appID, segment, active)
	exec("INSERT INTO notification_targets(application_id,notification_id,target_type,user_id) VALUES($1,$2,'INDIVIDUAL',$3)", appID, event.NotificationID, active)
	exec("INSERT INTO notification_targets(application_id,notification_id,target_type,segment_id) VALUES($1,$2,'SEGMENT',$3)", appID, event.NotificationID, segment)
	var results [2]dispatch.PreparationResult
	var errs [2]error
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 2 {
		wg.Go(func() { <-start; results[i], errs[i] = preparer.Prepare(ctx, event) })
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, results[0], results[1])
	require.Equal(t, dispatch.PreparationResult{SnapshotComplete: true, ResolvedUsers: 2, EligibleUsers: 1, EligibleDevices: 2}, results[0])
	require.EqualValues(t, 2, count("SELECT count(*) FROM notification_delivery_intents WHERE notification_id=$1 AND application_id=$2 AND owner_user_id=$3 AND attempt_count=0 AND delivery_version=0", event.NotificationID, appID, active))
	require.EqualValues(t, 1, count("SELECT count(*) FROM notifications WHERE id=$1 AND status='PROCESSING' AND preparation_hold_reason IS NULL", event.NotificationID))
	_, err = pool.Exec(ctx, "UPDATE notification_preparations SET eligible_device_count=eligible_device_count+1 WHERE notification_id=$1", event.NotificationID)
	require.Error(t, err, "complete denominators are immutable")
	_, err = pool.Exec(ctx, "UPDATE notification_delivery_intents SET owner_epoch=owner_epoch+1 WHERE notification_id=$1", event.NotificationID)
	require.Error(t, err, "intent attribution is immutable")
	commitRetry := seed(active)
	failedCommit, err := dispatch.NewPreparer(failOutboxCommit{pool})
	require.NoError(t, err)
	_, err = failedCommit.Prepare(ctx, commitRetry)
	require.ErrorContains(t, err, "injected commit failure")
	require.Zero(t, count("SELECT count(*) FROM notification_delivery_intents WHERE notification_id=$1", commitRetry.NotificationID))
	_, err = preparer.Prepare(ctx, commitRetry)
	require.NoError(t, err)
	require.EqualValues(t, 2, count("SELECT count(*) FROM notification_delivery_intents WHERE notification_id=$1", commitRetry.NotificationID))
	// Later enrollment and logout retain the original immutable denominator.
	enroll(appID, active, "prep-later-address")
	_, err = lifecycle.Apply(ctx, installation.Principal{ApplicationID: appID, UserID: active}, installation.Change{InstallationID: firstBinding.State.PublicID, Proof: firstBinding.Proof, ExpectedVersion: firstBinding.State.Version, Operation: installation.Logout})
	require.NoError(t, err)
	replay, err := preparer.Prepare(ctx, event)
	require.NoError(t, err)
	require.Equal(t, results[0], replay)

	empty := seed(inactive)
	result, err := preparer.Prepare(ctx, empty)
	require.NoError(t, err)
	require.Equal(t, dispatch.PreparationResult{SnapshotComplete: true, ResolvedUsers: 1}, result)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notifications WHERE id=$1 AND status='COMPLETED' AND completed_at IS NOT NULL", empty.NotificationID))

	uncovered := count("INSERT INTO users(application_id,external_id) VALUES($1,'prep-uncovered') RETURNING id", appID)
	held := seed(uncovered)
	_, err = preparer.Prepare(ctx, held)
	require.ErrorIs(t, err, dispatch.ErrAdmissionHeld)
	require.Zero(t, count("SELECT count(*) FROM notification_delivery_intents WHERE notification_id=$1", held.NotificationID))
	require.EqualValues(t, 1, count("SELECT count(*) FROM notifications WHERE id=$1 AND preparation_hold_reason='audience_not_ready'", held.NotificationID))
	// A coverage hold can recover under its original acceptance and deadline.
	enroll(appID, uncovered, "prep-recovered")
	result, err = preparer.Prepare(ctx, held)
	require.NoError(t, err)
	require.EqualValues(t, 1, result.EligibleDevices)

	bad := seed(active)
	changed := bad
	changed.DeadlineAt = changed.DeadlineAt.Add(time.Second)
	_, err = preparer.Prepare(ctx, changed)
	require.ErrorIs(t, err, dispatch.ErrAdmissionEnvelope)
	var otherProject pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, "SELECT public_id FROM applications WHERE id=$1", otherApp).Scan(&otherProject))
	changed = bad
	changed.ApplicationID = otherProject.String()
	_, err = preparer.Prepare(ctx, changed)
	require.ErrorIs(t, err, dispatch.ErrAdmissionEnvelope)
	changed = bad
	changed.SourceEpoch = otherProject.String()
	_, err = preparer.Prepare(ctx, changed)
	require.ErrorIs(t, err, dispatch.ErrAdmissionEnvelope)
	// Legacy CHECK permits extra fields; modern acceptance/preparation reject them.
	exec("UPDATE notification_targets SET segment_id=$2 WHERE notification_id=$1", bad.NotificationID, segment)
	_, err = preparer.Prepare(ctx, bad)
	require.ErrorIs(t, err, dispatch.ErrAdmissionTarget)
	require.Zero(t, count("SELECT count(*) FROM notification_delivery_intents WHERE notification_id=$1", bad.NotificationID))
	require.EqualValues(t, 1, count("SELECT count(*) FROM notifications WHERE id=$1 AND preparation_hold_reason='unsupported_targets' AND preparation_next_attempt_at>clock_timestamp()", bad.NotificationID))

	expired := seed(active)
	exec("UPDATE notifications SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", expired.NotificationID)
	require.NoError(t, pool.QueryRow(ctx, "SELECT deadline_at FROM notifications WHERE id=$1", expired.NotificationID).Scan(&expired.DeadlineAt))
	result, err = preparer.Prepare(ctx, expired)
	require.NoError(t, err)
	require.False(t, result.SnapshotComplete)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notifications WHERE id=$1 AND status='FAILED' AND lifecycle_reason='ttl_expired'", expired.NotificationID))
	require.Zero(t, count("SELECT count(*) FROM notification_preparations WHERE notification_id=$1", expired.NotificationID))
	partial := seed(active)
	exec("UPDATE notifications SET status='PROCESSING' WHERE id=$1", partial.NotificationID)
	_, err = preparer.Prepare(ctx, partial)
	require.ErrorIs(t, err, dispatch.ErrAdmissionState)

	// Recovery needs no outbox row or Kafka message and uses the same preparation.
	recovery, err := dispatch.NewQueuedRecovery(sqlc.New(pool), preparer)
	require.NoError(t, err)
	_, err = recovery.Tick(ctx, 0)
	require.Error(t, err)
	lostEvent := seed(active)
	report, err := recovery.Tick(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, dispatch.RecoveryReport{Visited: 1, Prepared: 1}, report)
	require.EqualValues(t, 2, count("SELECT count(*) FROM notification_delivery_intents WHERE notification_id=$1", lostEvent.NotificationID))
	report, err = recovery.Tick(ctx, 100)
	require.NoError(t, err)
	require.Zero(t, report.Visited, "successful work and future holds do not consume the next poll")
	newUncovered := count("INSERT INTO users(application_id,external_id) VALUES($1,'recovery-uncovered') RETURNING id", appID)
	seed(newUncovered)
	report, err = recovery.Tick(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, dispatch.RecoveryReport{Visited: 1, Held: 1}, report)
	late := seed(active)
	exec("UPDATE notifications SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", late.NotificationID)
	report, err = recovery.Tick(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, dispatch.RecoveryReport{Visited: 1, Terminal: 1}, report)
	sealedWork := seed(active)
	exec("UPDATE notification_runtime SET sealed=true")
	report, err = recovery.Tick(ctx, 100)
	require.NoError(t, err)
	require.Zero(t, report.Visited)
	exec("UPDATE notification_runtime SET sealed=false")
	exec("UPDATE notifications SET source_epoch=$2 WHERE id=$1", sealedWork.NotificationID, otherProject)
	report, err = recovery.Tick(ctx, 100)
	require.NoError(t, err)
	require.Zero(t, report.Visited, "old lineage is retained for explicit reconciliation")
	require.Zero(t, count("SELECT count(*) FROM notification_send_outbox"))
}
