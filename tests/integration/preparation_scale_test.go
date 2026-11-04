//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/kafka"
)

// This fixture measures the set-wise preparation transaction only, not address
// encryption, real mobile enrollment, broker throughput or provider delivery.
func TestAtomicPreparationTwentyThousandDevices(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	pool := acceptanceFixture(t, ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	exec("ALTER TABLE applications DROP CONSTRAINT applications_status_check")
	var appID, staffID, campaignID int64
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO applications(key,name,status) VALUES('scale-fixture','Scale','ACTIVE') RETURNING id").Scan(&appID))
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Scale','scale@fixture.invalid','fixture' FROM roles WHERE name='ADMIN' RETURNING id").Scan(&staffID))
	exec("INSERT INTO users(application_id,external_id) SELECT $1,'scale-'||g FROM generate_series(1,20000) g", appID)
	exec("INSERT INTO mobile_installations(application_id,owner_user_id,proof_verifier) SELECT application_id,id,decode(repeat('11',32),'hex') FROM users WHERE application_id=$1", appID)
	exec(`INSERT INTO installation_bindings(application_id,installation_id,owner_user_id,owner_epoch,platform,
        provider_namespace,provider_route_key,address_type,address_fingerprint,address_ciphertext,encryption_key_version)
        SELECT application_id,id,owner_user_id,owner_epoch,'ANDROID','scale-fixture','scale-route','FCM_REGISTRATION_TOKEN',
            sha256(convert_to(id::text,'UTF8')),decode(repeat('11',256),'hex'),1 FROM mobile_installations WHERE application_id=$1`, appID)
	exec("INSERT INTO application_dispatch_policy(application_id,hold_new,coverage_approved,push_ttl_seconds,key_ttl_seconds) VALUES($1,false,true,3600,86400)", appID)
	exec("UPDATE notification_runtime SET sealed=false,migration_hold=false")
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO notifications(application_id,title,body,created_by,status,queued_at,deadline_at,origin_epoch,source_epoch) SELECT $1,'Scale','Body',$2,'QUEUED',clock_timestamp(),clock_timestamp()+interval '1 hour',origin_epoch,source_epoch FROM notification_runtime RETURNING id", appID, staffID).Scan(&campaignID))
	exec("INSERT INTO notification_targets(application_id,notification_id,target_type) VALUES($1,$2,'BROADCAST')", appID, campaignID)
	var event kafka.ScopedSendRequested
	event.SchemaVersion, event.NotificationID = 1, campaignID
	require.NoError(t, pool.QueryRow(ctx, "SELECT a.public_id::text,n.queued_at,n.deadline_at,n.origin_epoch::text,n.source_epoch::text FROM notifications n JOIN applications a ON a.id=n.application_id WHERE n.id=$1", campaignID).Scan(&event.ApplicationID, &event.RequestedAt, &event.DeadlineAt, &event.OriginEpoch, &event.SourceEpoch))
	preparer, err := dispatch.NewPreparer(pool)
	require.NoError(t, err)
	start := time.Now()
	result, err := preparer.Prepare(ctx, event)
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.Equal(t, dispatch.PreparationResult{SnapshotComplete: true, ResolvedUsers: 20000, EligibleUsers: 20000, EligibleDevices: 20000}, result)
	var intents int64
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM notification_delivery_intents WHERE notification_id=$1", campaignID).Scan(&intents))
	require.EqualValues(t, 20000, intents)
	t.Logf("local 20k-user/20k-device atomic preparation: %s; excludes enrollment, encryption, Kafka, FCM and production load", elapsed)
}
