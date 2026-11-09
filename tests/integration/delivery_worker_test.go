//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/installation"
	"hello/internal/kafka"
)

// The isolated database role puts every pooled daemon connection in this test's schema.
func writeDeliveryWorkerPolicy(t *testing.T, ctx context.Context, f executionFixture, source dispatch.SourceManifest) {
	t.Helper()
	role := fmt.Sprintf("delivery_role_%d", time.Now().UnixNano())
	roleSQL := pgx.Identifier{role}.Sanitize()
	executionSQL(t, ctx, f, "CREATE ROLE "+roleSQL+" LOGIN SUPERUSER PASSWORD 'integration_only'")
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		var sessions int
		require.NoError(t, f.pool.QueryRow(cleanup, "SELECT count(*) FROM pg_stat_activity WHERE usename=$1", role).Scan(&sessions))
		require.Zero(t, sessions, "worker connections must close on shutdown")
		executionSQL(t, cleanup, f, "DROP ROLE "+roleSQL)
	})
	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	executionSQL(t, ctx, f, "ALTER ROLE "+roleSQL+" SET search_path TO "+pgx.Identifier{schema}.Sanitize()+",pg_catalog")
	directory := os.Getenv("HELLO_INTEGRATION_CERTS_DIR")
	require.NotEmpty(t, directory)
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(directory, name))
		require.NoError(t, err)
		return string(data)
	}
	uri := "postgresql://" + role + ":integration_only@postgres:5432/notification_integration?sslmode=verify-full&connect_timeout=3&sslrootcert=/etc/hello/ca.crt"
	policy := map[string]any{
		"mode": "delivery_only", "database_url": uri,
		"log_source_id": source.ID.String(), "cluster_identity": source.ClusterID, "topic_name": source.Topic,
		"topic_incarnation": source.Incarnation, "partition_count": source.PartitionCount, "retry_min_ms": 100, "retry_max_ms": 1000,
		"kafka": map[string]any{
			"brokers": []string{"kafka:9096"}, "authentication": "MTLS", "ca_pem": read("ca.crt"),
			"client_certificate_pem": read("client.crt"), "client_key_pem": read("client.key"), "sasl_username": "", "sasl_password": "",
		},
		"delivery": map[string]any{
			"application_id": f.project.String(), "environment": "fixture", "project_id": "integration-fixture",
			"routes":                []map[string]any{{"namespace": "execution-fixture", "key": "route", "platform": "ANDROID", "address_type": installation.FCMRegistrationToken}},
			"credential_project_id": "integration-fixture", "service_account_email": "fixture@integration-fixture.iam.gserviceaccount.com",
			"service_account_file": "/etc/hello/service-account.json", "cross_project_approved": false,
			"lease_seconds": 60, "renew_seconds": 15, "request_timeout_seconds": 10, "retry_floor_seconds": 10, "max_attempts": 1,
			"receipt_key_id": "fixture-key", "observation_window_seconds": 86400,
			"requests_per_second": 10, "project_requests_per_second": 10, "replica_count": 1, "concurrency": 2,
			"breaker_threshold": 5, "breaker_cooldown_seconds": 30, "poll_interval_ms": 100, "page_size": 1,
			"current_key_version": 1, "encryption_keys": []map[string]any{{"version": 1, "key": bytes.Repeat([]byte{1}, 32)}},
			"fingerprint_key": bytes.Repeat([]byte{2}, 32),
			"signing_keys":    []map[string]any{{"id": "fixture-key", "key": ed25519.NewKeyFromSeed(bytes.Repeat([]byte{11}, 32))}},
		},
	}
	data, err := json.Marshal(policy)
	require.NoError(t, err)
	cmd := deliveryWorkerExec(ctx, "sh", "-ec", "umask 027; cat > /etc/hello/worker.json; chmod 0640 /etc/hello/worker.json")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
}

func deliveryWorkerExec(ctx context.Context, args ...string) *exec.Cmd {
	compose := []string{"compose", "--env-file", "/dev/null", "-f", "../../compose.integration.yml", "-p", "hello-integration", "exec", "-T", "worker-fixture"}
	return exec.CommandContext(ctx, "docker", append(compose, args...)...)
}

func providerRequests(t *testing.T, ctx context.Context) (int, int) {
	t.Helper()
	output, err := exec.CommandContext(ctx, "docker", "compose", "--env-file", "/dev/null",
		"-f", "../../compose.integration.yml", "-p", "hello-integration", "logs", "--no-color", "provider-fixture").Output()
	require.NoError(t, err)
	return bytes.Count(output, []byte("accepted synthetic OAuth request")), bytes.Count(output, []byte("accepted synthetic FCM request"))
}

// The production binary reads its protected files and uses the fixture network's
// private Google endpoints. stdin and argv never carry the service-account key.
func runDeliveryWorkerBinary(t *testing.T, ctx context.Context, binary string) (context.Context, func()) {
	t.Helper()
	// The container image has unrelated PG* image metadata. The deployment
	// policy correctly rejects such environment overrides, so start with none.
	processCtx, stopProcess := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	t.Cleanup(stopProcess)
	cmd := deliveryWorkerExec(processCtx, "sh", "-ec", `echo $$ > /tmp/hello-worker.pid; exec env -i PATH=/usr/bin:/bin "$1"`, "worker", binary)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	require.NoError(t, cmd.Start())
	running, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		cancel()
	}()
	var once sync.Once
	shutdownWorker := func() {
		// The test context is cancelled before cleanup runs. Use an independent
		// deadline to deliver SIGTERM and wait for the actual process exit.
		shutdown, stop := context.WithTimeout(context.Background(), 8*time.Second)
		defer stop()
		select {
		case err := <-done:
			t.Fatalf("delivery worker exited before SIGTERM: %v; %s", err, logs.String())
		default:
		}
		signal := deliveryWorkerExec(shutdown, "sh", "-ec", `read -r pid < /tmp/hello-worker.pid; test "$(readlink /proc/$pid/exe)" = "$1"; kill -TERM "$pid"`, "worker", binary)
		output, err := signal.CombinedOutput()
		require.NoError(t, err, "signaling fixture worker: %s", output)
		select {
		case err := <-done:
			require.NoError(t, err, "worker SIGTERM exit: %s", logs.String())
		case <-shutdown.Done():
			t.Fatal("delivery worker failed to exit on SIGTERM")
		}
	}
	stopWorker := func() { once.Do(shutdownWorker) }
	t.Cleanup(stopWorker)
	return running, stopWorker
}

func TestDeliveryWorkerRecoversInterruptedClaimAndPublishesAfterHold(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	f := readySubmission(t, ctx)
	otherEvent := seedOtherWorkerEvent(t, ctx, f)
	policy := executionPolicy()
	policy.MaxAttempts = 1
	old, err := dispatch.NewExecution(f.pool, policy)
	require.NoError(t, err)
	lease, err := old.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	abandoned, eligible := f.deliveries[0], f.deliveries[1]
	_, err = old.Reserve(ctx, lease, abandoned)
	require.NoError(t, err)
	executionSQL(t, ctx, f, "UPDATE notifications SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", f.campaign)
	executionSQL(t, ctx, f, "UPDATE application_dispatch_policy SET hold_new=true WHERE application_id=$1", f.app)

	source := dispatch.SourceManifest{Topic: "notification.delivery.updated", Incarnation: "worker-fixture", PartitionCount: 1}
	transport := &broker.Transport{DialTimeout: time.Second}
	defer transport.CloseIdleConnections()
	client := &broker.Client{Addr: broker.TCP("127.0.0.1:19092"), Transport: transport, Timeout: 3 * time.Second}
	metadata, err := client.Metadata(ctx, &broker.MetadataRequest{Topics: []string{source.Topic}})
	require.NoError(t, err)
	source.ClusterID = metadata.ClusterID
	require.NoError(t, kafka.ValidateScopedMetadata(metadata, source.ClusterID, source.Topic, 1))
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notification_log_sources(
		id,topic,cluster_identity,topic_incarnation,active) VALUES(gen_random_uuid(),$1,$2,$3,true) RETURNING id`,
		source.Topic, source.ClusterID, source.Incarnation).Scan(&source.ID))
	writeDeliveryWorkerPolicy(t, ctx, f, source)
	reader := broker.NewReader(broker.ReaderConfig{Brokers: []string{"127.0.0.1:19092"}, Topic: source.Topic,
		Partition: 0, MaxWait: 100 * time.Millisecond, MinBytes: 1, MaxBytes: 65536})
	defer func() { require.NoError(t, reader.Close()) }()
	firstOffset := lastTopicOffset(t, ctx, source.Topic)
	require.NoError(t, reader.SetOffset(firstOffset))
	beforeOAuth, beforeFCM := providerRequests(t, ctx)
	runCtx, stopWorker := runDeliveryWorkerBinary(t, ctx, "/fixture-bin/worker")
	read := func(id pgtype.UUID, status string, offset int64) kafka.ScopedDeliveryUpdated {
		t.Helper()
		msg, err := reader.ReadMessage(runCtx)
		require.NoError(t, err)
		require.Equal(t, offset, msg.Offset)
		event, err := kafka.DecodeScopedDelivery(msg.Value)
		require.NoError(t, err)
		require.Equal(t, kafka.DeliveryUpdateKey(f.campaign), msg.Key)
		require.Equal(t, f.project.String(), event.ApplicationID)
		require.Equal(t, id.String(), event.DeliveryID)
		require.Equal(t, status, event.Status)
		require.EqualValues(t, 1, event.DeliveryVersion)
		return event
	}
	failed := read(abandoned, "FAILED", firstOffset)
	require.NotNil(t, failed.Reason)
	require.Equal(t, "retry_budget_exhausted", *failed.Reason)
	require.Nil(t, failed.ProviderMessageID)
	var attempts int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT attempt_count FROM notification_delivery_intents WHERE id=$1", eligible).Scan(&attempts))
	require.Zero(t, attempts)

	rollback := os.Getenv("HELLO_INTEGRATION_ROLLBACK_WORKER") != ""
	if rollback {
		// The prior worker must resume on the same database, held application,
		// source registration and Kafka partition after the current worker exits.
		stopWorker()
		runCtx, stopWorker = runDeliveryWorkerBinary(t, ctx, "/fixture-bin/worker-prior")
	}
	executionSQL(t, ctx, f, "UPDATE application_dispatch_policy SET hold_new=false WHERE application_id=$1", f.app)
	accepted := read(eligible, "SENT", firstOffset+1)
	require.NotNil(t, accepted.ProviderMessageID)
	require.Contains(t, *accepted.ProviderMessageID, "projects/integration-fixture/messages/accepted-")
	require.NotNil(t, accepted.ProviderAcceptedAt)
	require.False(t, accepted.ObservedDelivered)
	require.Eventually(t, func() bool {
		var complete, drained bool
		return f.pool.QueryRow(ctx, `SELECT status='COMPLETED' AND lease_owner IS NULL,
			NOT EXISTS (SELECT 1 FROM notification_update_outbox WHERE notification_id=n.id)
			FROM notifications n WHERE id=$1`, f.campaign).Scan(&complete, &drained) == nil && complete && drained
	}, 5*time.Second, 50*time.Millisecond)
	var count, other int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT (SELECT sum(attempt_count) FROM notification_delivery_intents WHERE notification_id=$1),
		(SELECT count(*) FROM notification_update_outbox WHERE event_id=$2)`, f.campaign, otherEvent).Scan(&count, &other))
	require.Equal(t, 2, count)
	require.Equal(t, 1, other)
	stopWorker()
	oauth, fcm := providerRequests(t, ctx)
	require.Equal(t, beforeOAuth+1, oauth)
	require.Equal(t, beforeFCM+1, fcm)
	require.Equal(t, firstOffset+2, lastTopicOffset(t, ctx, source.Topic))
	if rollback {
		require.Equal(t, failed.OriginEpoch, accepted.OriginEpoch)
		require.Equal(t, failed.SourceEpoch, accepted.SourceEpoch)
		require.NotEqual(t, failed.EventID, accepted.EventID)
		t.Log("Worker rollback passed on the current schema with two consecutive Kafka outcomes and one FCM request")
	}
}

// A second application has a real, publishable outbox event before the worker starts.
func seedOtherWorkerEvent(t *testing.T, ctx context.Context, f executionFixture) pgtype.UUID {
	t.Helper()
	var app, user int64
	var project pgtype.UUID
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO applications(key,name,status)
		VALUES('worker-other','Other worker application','ACTIVE') RETURNING id,public_id`).Scan(&app, &project))
	executionSQL(t, ctx, f, `INSERT INTO application_dispatch_policy(application_id,hold_new,coverage_approved,push_ttl_seconds,key_ttl_seconds)
		VALUES($1,false,true,300,300)`, app)
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO users(application_id,external_id) VALUES($1,'worker-other-user') RETURNING id", app).Scan(&user))
	_, err := f.lifecycle.Enroll(ctx, installation.Principal{ApplicationID: app, UserID: user},
		installation.Route{Namespace: "execution-fixture", Key: "route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}, "other-worker-address")
	require.NoError(t, err)
	approveFixtureBindings(t, ctx, f.pool, app)
	event := kafka.ScopedSendRequested{SchemaVersion: 1, ApplicationID: project.String()}
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notifications(application_id,title,body,created_by,status,queued_at,deadline_at,origin_epoch,source_epoch)
		SELECT $1,'Other','Other',$2,'QUEUED',clock_timestamp(),clock_timestamp()+interval '5 minutes',origin_epoch,source_epoch FROM notification_runtime
		RETURNING id,queued_at,deadline_at,origin_epoch::text,source_epoch::text`, app, f.staff).Scan(&event.NotificationID, &event.RequestedAt, &event.DeadlineAt, &event.OriginEpoch, &event.SourceEpoch))
	executionSQL(t, ctx, f, "INSERT INTO notification_targets(application_id,notification_id,target_type) VALUES($1,$2,'BROADCAST')", app, event.NotificationID)
	preparer, err := dispatch.NewPreparer(f.pool)
	require.NoError(t, err)
	_, err = preparer.Prepare(ctx, event)
	require.NoError(t, err)
	s := executionService(t, f.pool)
	lease, err := s.Acquire(ctx, project, event.NotificationID)
	require.NoError(t, err)
	executionSQL(t, ctx, f, "UPDATE notifications SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", event.NotificationID)
	var id, eventID pgtype.UUID
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT id FROM notification_delivery_intents WHERE application_id=$1", app).Scan(&id))
	changed, err := s.RecoverDelivery(ctx, lease, id)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT event_id FROM notification_update_outbox WHERE application_id=$1", app).Scan(&eventID))
	return eventID
}
