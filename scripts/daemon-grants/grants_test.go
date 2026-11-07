//go:build integration

package daemongrants

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"hello/internal/database/sqlc"
	"hello/internal/dispatch"
	"hello/internal/features/applications"
	"hello/internal/kafka"
	"hello/internal/receipt"
)

type publisher struct {
	send   kafka.ScopedSendRequested
	update []byte
}

func (p *publisher) PublishScopedSend(_ context.Context, event kafka.ScopedSendRequested) error {
	p.send = event
	return nil
}

func (p *publisher) PublishScopedDelivery(_ context.Context, data []byte) error {
	p.update = append([]byte(nil), data...)
	return nil
}

func connect(t *testing.T, role string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(t.Context(), "postgres://"+role+"@/postgres?host=/var/run/postgresql&sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(p.Close)
	var session, active string
	require.NoError(t, p.QueryRow(t.Context(), "SELECT session_user,current_user").Scan(&session, &active))
	require.Equal(t, role, session)
	require.Equal(t, role, active)
	return p
}

func run(t *testing.T, p *pgxpool.Pool, stmt string, args ...any) {
	t.Helper()
	_, err := p.Exec(t.Context(), stmt, args...)
	require.NoError(t, err)
}

func denied(t *testing.T, p *pgxpool.Pool, code, stmt string) {
	t.Helper()
	_, err := p.Exec(t.Context(), stmt)
	var pe *pgconn.PgError
	require.ErrorAs(t, err, &pe, stmt)
	require.Equal(t, code, pe.Code, stmt)
}

func TestProtectedDatabaseRoles(t *testing.T) {
	if os.Getenv("HELLO_DAEMON_GRANTS_FIXTURE") != "1" {
		t.Skip("run scripts/daemon-grants.sh in the isolated fixture")
	}
	ctx := t.Context()
	owner := connect(t, "fixture_owner")
	scheduler := connect(t, "fixture_scheduler")
	admission := connect(t, "fixture_admission")
	delivery := connect(t, "fixture_delivery")
	outsider := connect(t, "fixture_unrelated")
	run(t, owner, `ALTER TABLE applications DROP CONSTRAINT applications_status_check;
INSERT INTO roles(name) VALUES('local-db-grants');
INSERT INTO staff_users(id,role_id,name,email,password_hash)
SELECT 901,id,'fixture','grants@fixture.invalid','disabled-fixture' FROM roles WHERE name='local-db-grants';
INSERT INTO applications(id,key,name,status) VALUES(901,'grants-fixture','DB grants','ACTIVE');
INSERT INTO application_dispatch_policy(application_id,hold_new,coverage_approved,push_ttl_seconds,key_ttl_seconds) VALUES(901,false,true,3600,3600);
UPDATE notification_runtime SET sealed=false,migration_hold=false;
INSERT INTO users(id,application_id,external_id) VALUES(901,901,'grant-fixture');
INSERT INTO mobile_installations(id,application_id,owner_user_id,proof_verifier)
VALUES(901,901,901,sha256('synthetic-proof'::bytea));
INSERT INTO installation_bindings(id,application_id,installation_id,owner_user_id,owner_epoch,platform,provider_namespace,
    provider_route_key,address_type,address_fingerprint,address_ciphertext,encryption_key_version)
VALUES(901,901,901,901,1,'ANDROID','fixture','fixture','FCM_REGISTRATION_TOKEN',sha256('fixture'::bytea),'synthetic-only',1);
INSERT INTO notifications(id,application_id,title,body,status,created_by,scheduled_at,deadline_at,origin_epoch,source_epoch)
SELECT 901,901,'fixture','fixture','SCHEDULED',901,now()-interval '2 seconds',now()+interval '1 hour',origin_epoch,source_epoch FROM notification_runtime;
INSERT INTO notification_targets(notification_id,application_id,target_type) VALUES(901,901,'BROADCAST');
`)
	var routed bool
	require.NoError(t, owner.QueryRow(ctx, "SELECT to_regclass('current_binding_routes') IS NOT NULL").Scan(&routed))
	if routed {
		provision, err := os.ReadFile("/fixtures/active-binding-routes.sql")
		require.NoError(t, err)
		_, err = owner.Exec(ctx, string(provision), int64(901))
		if pe, ok := err.(*pgconn.PgError); ok {
			t.Fatalf("synthetic cohort seed failed: %s / %s", pe.ConstraintName, pe.Detail)
		}
		require.NoError(t, err)
	}
	var project pgtype.UUID
	require.NoError(t, owner.QueryRow(ctx, "SELECT public_id FROM applications WHERE id=901").Scan(&project))
	source := func(topic string) dispatch.SourceManifest {
		s := dispatch.SourceManifest{ClusterID: "fixture", Topic: topic, Incarnation: "fixture-v1", PartitionCount: 1}
		require.NoError(t, owner.QueryRow(ctx, `INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active)
VALUES(gen_random_uuid(),$1,$2,$3,true) RETURNING id`, topic, s.ClusterID, s.Incarnation).Scan(&s.ID))
		return s
	}
	send, update := source("notification.send.requested"), source("notification.delivery.updated")
	workload, err := os.ReadFile("/workload.sql")
	require.NoError(t, err)
	for _, p := range []*pgxpool.Pool{scheduler, admission, delivery} {
		require.NoError(t, send.VerifyRegistration(ctx, p))
		run(t, p, string(workload))
		run(t, p, "SELECT min(next_publish_at) FROM notification_send_outbox")
		run(t, p, "SELECT min(next_attempt_at) FROM notification_update_outbox")
		var held bool
		require.NoError(t, p.QueryRow(ctx, `SELECT sealed OR migration_hold FROM notification_runtime
WHERE singleton AND to_regclass('notification_observations') IS NOT NULL AND to_regclass('notification_send_outbox') IS NOT NULL AND to_regclass('notification_update_outbox') IS NOT NULL`).Scan(&held))
		require.False(t, held)
	}
	service := applications.NewService(sqlc.New(scheduler), scheduler)
	changed, err := service.PromoteSchedule(ctx, project, 901)
	require.NoError(t, err)
	require.True(t, changed)
	pub := new(publisher)
	outbox, err := dispatch.NewSourceOutbox(scheduler, pub, send)
	require.NoError(t, err)
	outcome, err := outbox.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)
	preparer, err := dispatch.NewPreparer(admission)
	require.NoError(t, err)
	prepared, err := preparer.Prepare(ctx, pub.send)
	require.NoError(t, err)
	require.EqualValues(t, 1, prepared.EligibleDevices)
	recovery, err := dispatch.NewPreparer(scheduler)
	require.NoError(t, err)
	_, err = recovery.Prepare(ctx, pub.send)
	require.NoError(t, err)
	q, err := dispatch.NewQuarantine(admission)
	require.NoError(t, err)
	bad := dispatch.SourceMessage{LogSourceID: send.ID, Topic: send.Topic, Key: []byte("fixture"), Payload: []byte("synthetic")}
	require.NoError(t, q.Store(ctx, bad, dispatch.InvalidEnvelope, 1))
	require.NoError(t, q.Store(ctx, bad, dispatch.InvalidEnvelope, 1))
	run(t, owner, `INSERT INTO notification_replay_requests(application_id,id,staff_user_id,operational_grant_id,selection_hash,change_reference)
VALUES(901,'10000000-0000-4000-8000-000000000001',901,gen_random_uuid(),sha256('fixture'::bytea),'fixture');
INSERT INTO notification_replay_attempts(application_id,request_id,log_source_id,original_partition,original_offset,
    notification_id,origin_epoch,source_epoch,claim_token,created_at,lease_expires_at,outcome)
SELECT 901,'10000000-0000-4000-8000-000000000001',s.id,0,0,901,r.origin_epoch,r.source_epoch,
    '10000000-0000-4000-8000-000000000002',now()-interval '31 seconds',now()-interval '1 second','started'
FROM notification_runtime r CROSS JOIN notification_log_sources s WHERE s.topic='notification.send.requested';
INSERT INTO notification_replay_controls(log_source_id,original_partition,original_offset,active_application_id,active_request_id,active_claim_token)
SELECT log_source_id,0,0,application_id,request_id,claim_token FROM notification_replay_attempts;`)
	replay, err := dispatch.NewReplayRecovery(scheduler)
	require.NoError(t, err)
	report, err := replay.Tick(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, report.Abandoned)
	var audits int
	require.NoError(t, owner.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE application_id=901").Scan(&audits))
	require.Equal(t, 1, audits)

	var device pgtype.UUID
	require.NoError(t, owner.QueryRow(ctx, "SELECT id FROM notification_delivery_intents WHERE notification_id=901").Scan(&device))
	execution, err := dispatch.NewExecution(delivery, dispatch.ExecutionPolicy{LeaseDuration: time.Minute, RetryFloor: 10 * time.Second, MaxAttempts: 1,
		Receipt: receipt.Policy{Environment: "fixture", KeyID: "fixture-key", ObservationWindow: 24 * time.Hour}})
	require.NoError(t, err)
	lease, err := execution.Acquire(ctx, project, 901)
	require.NoError(t, err)
	_, err = execution.Reserve(ctx, lease, device)
	require.NoError(t, err)
	require.NoError(t, execution.Release(ctx, lease))
	lease, err = execution.Acquire(ctx, project, 901)
	require.NoError(t, err)
	changed, err = execution.RecoverDelivery(ctx, lease, device)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = execution.FinalizeCampaign(ctx, lease)
	require.NoError(t, err)
	require.True(t, changed)
	updates, err := dispatch.NewApplicationUpdateOutbox(delivery, pub, update, project)
	require.NoError(t, err)
	outcome, err = updates.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)
	require.NotEmpty(t, pub.update)
	require.NoError(t, sqlc.New(delivery).HoldResultDispatch(ctx, 901))
	var held bool
	require.NoError(t, owner.QueryRow(ctx, "SELECT hold_new FROM application_dispatch_policy WHERE application_id=901").Scan(&held))
	require.True(t, held)

	for _, p := range []*pgxpool.Pool{scheduler, admission, delivery} {
		if routed {
			denied(t, p, "42501", "SELECT * FROM binding_route_grants")
			denied(t, p, "42501", "INSERT INTO binding_route_grants DEFAULT VALUES")
			denied(t, p, "42501", "SELECT apply_binding_route(NULL,NULL)")
		}
		for _, query := range []string{"UPDATE notification_runtime SET sealed=false", "UPDATE notification_runtime SET migration_hold=false",
			"UPDATE application_dispatch_policy SET hold_new=false", "DELETE FROM application_dispatch_policy", "DELETE FROM notification_runtime",
			"UPDATE notification_log_sources SET active=true", "UPDATE installation_bindings SET owner_user_id=901", "UPDATE mobile_installations SET owner_epoch=2",
			"SELECT proof_verifier FROM mobile_installations", "SELECT password_hash FROM staff_users", "DELETE FROM notification_preparations",
			"UPDATE staff_users SET is_active=false", "INSERT INTO notification_replay_grants DEFAULT VALUES", "DELETE FROM audit_logs",
			"TRUNCATE notifications", "ALTER TABLE notifications ADD COLUMN unauthorized int", "CREATE TABLE public.unauthorized(id int)"} {
			denied(t, p, "42501", query)
		}
		denied(t, p, "23514", "UPDATE notification_runtime SET singleton=false")
		denied(t, p, "23514", "UPDATE applications SET id=902 WHERE id=901")
		denied(t, p, "23514", "UPDATE application_dispatch_policy SET application_id=902 WHERE application_id=901")
	}
	for _, p := range []*pgxpool.Pool{scheduler, admission} {
		denied(t, p, "42501", "SELECT payload FROM notifications")
		denied(t, p, "42501", "SELECT address_ciphertext FROM installation_bindings")
		denied(t, p, "42501", "DELETE FROM notification_update_outbox")
		denied(t, p, "42501", "UPDATE notification_delivery_intents SET attempt_count=1")
		denied(t, p, "42501", "SELECT hold_delivery_dispatch(901)")
	}
	for _, p := range []*pgxpool.Pool{admission, delivery} {
		denied(t, p, "42501", "DELETE FROM notification_send_outbox")
		denied(t, p, "42501", "INSERT INTO audit_logs DEFAULT VALUES")
		denied(t, p, "42501", "SELECT clear_finished_replay_claim(1,NULL,NULL,0,0,NULL)")
	}
	denied(t, scheduler, "42501", "SELECT original_payload FROM notification_send_dead_letters")
	denied(t, scheduler, "42501", "SELECT after_json FROM audit_logs")
	denied(t, scheduler, "42501", "UPDATE notification_replay_attempts SET outcome='published'")
	denied(t, delivery, "42501", "INSERT INTO notification_delivery_intents DEFAULT VALUES")
	denied(t, delivery, "23514", "UPDATE users SET id=902")
	denied(t, scheduler, "23514", "UPDATE staff_users SET id=902")
	run(t, outsider, "SELECT name FROM applications") // Pre-existing unrelated access survives every apply.
	denied(t, outsider, "42501", "SELECT sealed FROM notification_runtime")
	denied(t, outsider, "42501", "SELECT hold_delivery_dispatch(901)")
}
