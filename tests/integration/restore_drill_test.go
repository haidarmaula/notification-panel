//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
	"hello/internal/dispatch"
	"hello/internal/kafka"
)

// Only synthetic identities, counts and row digests cross the recovery boundary.
// The full dump stays in runner memory and the isolated target's tmpfs.
type restoreManifest struct {
	Send          kafka.ScopedSendRequested
	Source        dispatch.SourceManifest
	Schema        string
	FirstEvent    string
	AfterBackupID int64
	Tables        map[string]restoreTable
}

type restoreTable struct {
	Rows   int
	Digest string
}

func snapshotRestoreTables(t *testing.T, ctx context.Context, pool sqlc.DBTX) map[string]restoreTable {
	t.Helper()
	tables := make(map[string]restoreTable)
	for _, relation := range []string{"applications", "notifications", "notification_preparations", "users",
		"mobile_identities", "mobile_installations", "installation_bindings", "notification_delivery_intents",
		"notification_receipt_metadata", "notification_receipt_requests", "notification_observations",
		"notification_reads", "notification_log_sources", "notification_update_outbox"} {
		var state restoreTable
		query := `SELECT count(*), encode(sha256(convert_to(coalesce(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text), '[]')::text,'UTF8')), 'hex') FROM ` + pgx.Identifier{relation}.Sanitize() + " AS r"
		require.NoError(t, pool.QueryRow(ctx, query).Scan(&state.Rows, &state.Digest))
		tables[relation] = state
	}
	return tables
}

func restoreDocker(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = bytes.NewReader(input)
	return cmd.Output()
}

func requireRestoreDocker(t *testing.T, ctx context.Context, input []byte, args ...string) []byte {
	t.Helper()
	out, err := restoreDocker(ctx, input, args...)
	// SQL diagnostic context can include row data. Keep fixture data off stdout.
	require.NoError(t, err, "docker %s", args[0])
	return out
}

func TestSealedBackupRestoreDrill(t *testing.T) {
	probe := os.Getenv("HELLO_INTEGRATION_RESTORE_PROBE")
	if probe == "" {
		t.Skip("opt in with bash scripts/restore-drill.sh")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	f := newObservationFixture(t, ctx, "execution-address-0")
	_, err := f.worker.RecordResult(ctx, f.result)
	require.NoError(t, err)
	require.NoError(t, f.record(ctx, f.report))

	m := restoreManifest{Source: dispatch.SourceManifest{
		Topic: "notification.delivery.updated", Incarnation: "restore-fixture", PartitionCount: 1,
	}, Send: kafka.ScopedSendRequested{SchemaVersion: 1, ApplicationID: f.project.String(), NotificationID: f.campaign}}
	client := broker.Client{Addr: broker.TCP("127.0.0.1:19092")}
	meta, err := client.Metadata(ctx, &broker.MetadataRequest{Topics: []string{m.Source.Topic}})
	require.NoError(t, err)
	m.Source.ClusterID = meta.ClusterID
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active)
		VALUES(gen_random_uuid(),$1,$2,$3,true) RETURNING id`, m.Source.Topic, m.Source.ClusterID, m.Source.Incarnation).Scan(&m.Source.ID))
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT origin_epoch::text,source_epoch::text,queued_at,deadline_at
		FROM notifications WHERE id=$1`, f.campaign).Scan(&m.Send.OriginEpoch, &m.Send.SourceEpoch, &m.Send.RequestedAt, &m.Send.DeadlineAt))
	m.FirstEvent = f.projections(t, ctx)[0].EventID
	m.Tables = snapshotRestoreTables(t, ctx, f.pool)
	for name, n := range map[string]int{"notification_preparations": 1, "notification_delivery_intents": 2,
		"notification_update_outbox": 2, "notification_receipt_metadata": 1, "notification_receipt_requests": 1,
		"notification_observations": 1, "notification_reads": 1} {
		require.Equal(t, n, m.Tables[name].Rows, name)
	}
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&m.Schema))
	require.Regexp(t, `^acceptance_[0-9]+$`, m.Schema)
	compose := []string{"compose", "--env-file", "/dev/null", "-f", "../../compose.integration.yml", "-p", "hello-integration"}
	pg := strings.TrimSpace(string(requireRestoreDocker(t, ctx, nil, append(compose, "ps", "-q", "postgres")...)))
	require.Regexp(t, `^[0-9a-f]{12,64}$`, pg)
	image := strings.TrimSpace(string(requireRestoreDocker(t, ctx, nil, "inspect", "--format", "{{.Image}}", pg)))
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, image)
	start := time.Now()
	dump := requireRestoreDocker(t, ctx, nil, "exec", pg, "pg_dump", "-U", "integration", "-d", "notification_integration",
		"--schema", m.Schema, "--no-owner", "--no-privileges", "--format=plain")
	dumped := time.Now()

	// Kafka and the business ID allocator advance after the recovery point.
	before := lastTopicOffset(t, ctx, m.Source.Topic)
	producer := kafka.NewScopedProducer("127.0.0.1:19092", m.Source.Topic)
	defer func() { require.NoError(t, producer.Close()) }()
	outbox, err := dispatch.NewUpdateOutbox(f.pool, producer, m.Source)
	require.NoError(t, err)
	sent, err := outbox.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, sent)
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notifications(application_id,title,body,created_by)
		VALUES($1,'Post-cut canary','Synthetic',$2) RETURNING id`, f.app, f.staff).Scan(&m.AfterBackupID))
	after := lastTopicOffset(t, ctx, m.Source.Topic)
	require.Equal(t, before+1, after)
	reader := broker.NewReader(broker.ReaderConfig{Brokers: []string{"127.0.0.1:19092"}, Topic: m.Source.Topic, Partition: 0, MaxWait: 100 * time.Millisecond})
	defer func() { require.NoError(t, reader.Close()) }()
	require.NoError(t, reader.SetOffset(before))
	msg, err := reader.ReadMessage(ctx)
	require.NoError(t, err)
	event, err := kafka.DecodeScopedDelivery(msg.Value)
	require.NoError(t, err)
	require.Equal(t, before, msg.Offset)
	require.Equal(t, m.FirstEvent, event.EventID)
	require.Equal(t, m.Send.SourceEpoch, event.SourceEpoch)

	down, err := filepath.Abs("../../db/migrations/000031_create_receipt_observations.down.sql")
	require.NoError(t, err)
	restoreStart := time.Now()
	target := strings.TrimSpace(string(requireRestoreDocker(t, ctx, nil, "create", "--pull=never", "--network", "none",
		"--label", "hello.integration.restore="+filepath.Base(filepath.Dir(probe)),
		"--tmpfs", "/var/lib/postgresql/data", "-v", probe+":/restore-probe.test:ro", "-v", down+":/retained-receipts.down.sql:ro",
		"-e", "POSTGRES_HOST_AUTH_METHOD=trust", "-e", "POSTGRES_USER=integration", "-e", "POSTGRES_DB=restore_drill", image)))
	require.Regexp(t, `^[0-9a-f]{64}$`, target)
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		requireRestoreDocker(t, cleanup, nil, "rm", "-f", "-v", target)
	})
	requireRestoreDocker(t, ctx, nil, "start", target)
	require.Equal(t, "none", strings.TrimSpace(string(requireRestoreDocker(t, ctx, nil, "inspect", "--format", "{{.HostConfig.NetworkMode}}", target))))
	require.Eventually(t, func() bool {
		_, err := restoreDocker(ctx, nil, "exec", target, "pg_isready", "-h", "127.0.0.1", "-U", "integration", "-d", "restore_drill")
		return err == nil
	}, 20*time.Second, 200*time.Millisecond)
	// The target has no route to other containers or the host. The dump and seal
	// become visible together, even if the archived policy had sending enabled.
	targetSchema := pgx.Identifier{m.Schema}.Sanitize()
	seal := "ALTER DATABASE restore_drill SET search_path TO " + targetSchema + ",pg_catalog; UPDATE " + targetSchema + ".notification_runtime SET sealed=true,migration_hold=true;"
	requireRestoreDocker(t, ctx, dump, "exec", "-i", target, "psql", "-X", "-q", "--single-transaction", "-v", "ON_ERROR_STOP=1",
		"-U", "integration", "-d", "restore_drill", "-f", "-", "-c", seal)
	restored := time.Now()
	manifest, err := json.Marshal(m)
	require.NoError(t, err)
	output, err := restoreDocker(ctx, manifest, "exec", "-i", "-e", "HELLO_INTEGRATION=1", "-e", "HELLO_SEALED_PROBE=1", target,
		"/restore-probe.test", "-test.run=^TestRestoreIsolatedProbe$", "-test.timeout=20s")
	require.NoError(t, err, "%s", output)
	require.Contains(t, string(output), "PASS")
	require.Equal(t, after, lastTopicOffset(t, ctx, m.Source.Topic))
	t.Logf("Sealed restore passed. dump_sha256=%x dump_bytes=%d backup=%s restore=%s recovery_through_validation=%s elapsed=%s partition=0 kafka_end_before=%d kafka_end_after=%d retained_outbox=2",
		sha256.Sum256(dump), len(dump), dumped.Sub(start), restored.Sub(restoreStart), time.Since(restoreStart), time.Since(start), before, after)
}

type restoreProbeTx struct{ pgx.Tx }

func (t restoreProbeTx) BeginTx(ctx context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	return t.Begin(ctx)
}

func TestRestoreIsolatedProbe(t *testing.T) {
	if os.Getenv("HELLO_SEALED_PROBE") != "1" {
		t.Skip("executed inside the network-isolated restore target")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var m restoreManifest
	require.NoError(t, json.NewDecoder(os.Stdin).Decode(&m))
	pool, err := pgxpool.New(ctx, "postgres://integration@/restore_drill?host=/var/run/postgresql&sslmode=disable")
	require.NoError(t, err)
	defer pool.Close()
	verify := func() {
		t.Helper()
		require.Equal(t, m.Tables, snapshotRestoreTables(t, ctx, pool))
		var held bool
		var origin, source string
		require.NoError(t, pool.QueryRow(ctx, "SELECT sealed AND migration_hold,origin_epoch::text,source_epoch::text FROM notification_runtime").Scan(&held, &origin, &source))
		require.True(t, held)
		require.Equal(t, m.Send.OriginEpoch, origin)
		require.Equal(t, m.Send.SourceEpoch, source)
	}
	verify()
	var seen bool
	var sequence int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS(SELECT FROM notifications WHERE id=$1),last_value FROM notifications_id_seq`, m.AfterBackupID).Scan(&seen, &sequence))
	require.False(t, seen)
	require.Less(t, sequence, m.AfterBackupID)
	var status, id string
	var version, attempts int
	require.NoError(t, pool.QueryRow(ctx, `SELECT d.status,d.delivery_version,d.attempt_count,o.event_id::text FROM notification_delivery_intents d
		JOIN notification_update_outbox o ON o.delivery_id=d.id AND o.delivery_version=1
		WHERE d.notification_id=$1 AND d.provider_accepted_at IS NOT NULL AND d.observed_opened_at IS NOT NULL`, m.Send.NotificationID).Scan(&status, &version, &attempts, &id))
	require.Equal(t, "OPENED", status)
	require.Equal(t, 2, version)
	require.Equal(t, 1, attempts)
	require.Equal(t, m.FirstEvent, id)
	checkFences := func(db dispatch.Transactor, rejection error, expected dispatch.Outcome) {
		t.Helper()
		p, err := dispatch.NewPreparer(db)
		require.NoError(t, err)
		_, err = p.Prepare(ctx, m.Send)
		require.ErrorIs(t, err, rejection)
		outbox, err := dispatch.NewUpdateOutbox(db, updatePublishFunc(func(context.Context, []byte) error {
			t.Fatal("restore attempted external publication")
			return nil
		}), m.Source)
		require.NoError(t, err)
		outcome, err := outbox.DrainOne(ctx)
		require.NoError(t, err)
		require.Equal(t, expected, outcome)
	}
	require.ErrorIs(t, m.Source.VerifyRegistration(ctx, pool), dispatch.ErrRuntimeSealed)
	checkFences(pool, dispatch.ErrAdmissionHeld, dispatch.Held)
	// Probe a candidate lineage using savepoints underneath real service entry
	// points. Roll back the entire activation before it can become visible.
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var candidate pgtype.UUID
	require.NoError(t, tx.QueryRow(ctx, "UPDATE notification_runtime SET sealed=false,migration_hold=false,source_epoch=gen_random_uuid() RETURNING source_epoch").Scan(&candidate))
	require.NotEqual(t, m.Send.SourceEpoch, candidate.String())
	require.NoError(t, m.Source.VerifyRegistration(ctx, restoreProbeTx{tx}))
	checkFences(restoreProbeTx{tx}, dispatch.ErrAdmissionEnvelope, dispatch.Idle)
	require.Equal(t, m.Tables, snapshotRestoreTables(t, ctx, tx))
	require.NoError(t, tx.Rollback(ctx))
	down, err := os.ReadFile("/retained-receipts.down.sql")
	require.NoError(t, err)
	// This repository's down migrations address the production public schema.
	// Move the recovered fixture schema only within the aborting rollback trial.
	require.Regexp(t, `^acceptance_[0-9]+$`, m.Schema)
	trial, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = trial.Rollback(ctx) }()
	_, err = trial.Exec(ctx, "ALTER SCHEMA public RENAME TO empty_restore_schema; ALTER SCHEMA "+pgx.Identifier{m.Schema}.Sanitize()+" RENAME TO public")
	require.NoError(t, err)
	_, err = trial.Exec(ctx, string(down))
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "23514", pgErr.Code)
	require.NoError(t, trial.Rollback(ctx))
	verify()
}
