//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/kafka"
)

type updatePublishFunc func(context.Context, []byte) error

func (f updatePublishFunc) PublishScopedDelivery(ctx context.Context, value []byte) error {
	return f(ctx, value)
}
func newUpdateFixture(t *testing.T, ctx context.Context) (resultFixture, dispatch.SourceManifest) {
	t.Helper()
	f := newResultFixture(t, ctx, "execution-address-0")
	_, err := f.service.RecordResult(ctx, f.result)
	require.NoError(t, err)
	source := dispatch.SourceManifest{ClusterID: "fixture-update", Topic: "notification.delivery.updated", Incarnation: "fixture-generation", PartitionCount: 1}
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active) VALUES(gen_random_uuid(),$1,$2,$3,true) RETURNING id`, source.Topic, source.ClusterID, source.Incarnation).Scan(&source.ID))
	return f, source
}
func updateService(t *testing.T, f resultFixture, source dispatch.SourceManifest, p dispatch.UpdatePublisher) *dispatch.UpdateOutbox {
	t.Helper()
	o, err := dispatch.NewUpdateOutbox(f.db, p, source)
	require.NoError(t, err)
	return o
}
func addNextUpdate(t *testing.T, ctx context.Context, f resultFixture, sourceEpoch ...pgtype.UUID) {
	t.Helper()
	event, _ := f.event(t, ctx)
	if len(sourceEpoch) > 0 {
		event.SourceEpoch = sourceEpoch[0].String()
	}
	event.EventID = replayUUID(t, ctx, f.pool).String()
	event.DeliveryVersion++
	event.Status = "OPENED"
	event.EventKind = "observation"
	event.ObservedOpened = true
	event.ObservedDelivered = true
	event.OccurredAt = event.OccurredAt.Add(time.Microsecond)
	require.NoError(t, event.Validate())
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	executionSQL(t, ctx, f.executionFixture, `INSERT INTO notification_update_outbox(event_id,application_id,notification_id,delivery_id,delivery_version,origin_epoch,source_epoch,payload,occurred_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, event.EventID, f.app, f.campaign, f.delivery, event.DeliveryVersion, event.OriginEpoch, event.SourceEpoch, raw, event.OccurredAt)
}
func TestUpdateOutboxLockedPredecessorBlocksSuccessor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f, source := newUpdateFixture(t, ctx)
	addNextUpdate(t, ctx, f)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	publisher := updatePublishFunc(func(ctx context.Context, value []byte) error {
		event, err := kafka.DecodeScopedDelivery(value)
		if err != nil {
			return err
		}
		calls.Add(1)
		if event.DeliveryVersion == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	first, second := updateService(t, f, source, publisher), updateService(t, f, source, publisher)
	done := make(chan error, 1)
	go func() { _, err := first.DrainOne(ctx); done <- err }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("publisher never started: %v", err)
	case <-ctx.Done():
		t.Fatal("publisher start timed out")
	}
	outcome, err := second.DrainOne(ctx)
	close(release)
	require.NoError(t, <-done)
	require.NoError(t, err)
	require.Equal(t, dispatch.Idle, outcome)
	require.EqualValues(t, 1, calls.Load())
	outcome, err = second.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)
	require.EqualValues(t, 2, calls.Load())
}
func TestUpdateOutboxFailureDefersOnlyItsDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f, source := newUpdateFixture(t, ctx)
	addNextUpdate(t, ctx, f)
	var other pgtype.UUID
	for _, id := range f.deliveries {
		if id != f.delivery {
			other = id
		}
	}
	c := submissionClient(t, f.executionFixture, submissionAccess)
	result, err := submissionSender(t, f.service, c).Send(ctx, f.lease, other)
	require.NoError(t, err)
	_, err = f.service.RecordResult(ctx, result)
	require.NoError(t, err)
	o := updateService(t, f, source, updatePublishFunc(func(_ context.Context, value []byte) error {
		event, err := kafka.DecodeScopedDelivery(value)
		require.NoError(t, err)
		if event.DeliveryID == f.delivery.String() {
			require.EqualValues(t, 1, event.DeliveryVersion)
			return errors.New("uncertain broker write")
		}
		require.Equal(t, other.String(), event.DeliveryID)
		return nil
	}))
	outcome, err := o.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Deferred, outcome)
	outcome, err = o.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)
	outcome, err = o.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Idle, outcome)
	var attempts int
	var future bool
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT attempts,next_attempt_at>clock_timestamp() FROM notification_update_outbox WHERE delivery_id=$1 AND delivery_version=1`, f.delivery).Scan(&attempts, &future))
	require.Equal(t, 1, attempts)
	require.True(t, future)
}
func TestUpdateOutboxAckRollbackAndLostCommit(t *testing.T) {
	for _, mode := range []string{"rollback after ack", "lost commit ack"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			f, source := newUpdateFixture(t, ctx)
			_, original := f.event(t, ctx)
			var payloads [][]byte
			o := updateService(t, f, source, updatePublishFunc(func(_ context.Context, value []byte) error {
				payloads = append(payloads, bytes.Clone(value))
				return nil
			}))
			if mode == "rollback after ack" {
				executionSQL(t, ctx, f.executionFixture, `CREATE FUNCTION reject_update_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture rejection'; END; $$;
   CREATE TRIGGER reject_update_delete BEFORE DELETE ON notification_update_outbox FOR EACH ROW EXECUTE FUNCTION reject_update_delete()`)
			} else {
				f.db.uncertain.Store(true)
			}
			outcome, err := o.DrainOne(ctx)
			require.Error(t, err)
			require.Empty(t, outcome)
			require.Len(t, payloads, 1)
			require.Equal(t, original, payloads[0])
			if mode == "rollback after ack" {
				executionSQL(t, ctx, f.executionFixture, `DROP TRIGGER reject_update_delete ON notification_update_outbox; DROP FUNCTION reject_update_delete()`)
				outcome, err = o.DrainOne(ctx)
				require.NoError(t, err)
				require.Equal(t, dispatch.Published, outcome)
				require.Len(t, payloads, 2)
				require.Equal(t, payloads[0], payloads[1])
			} else {
				outcome, err = o.DrainOne(ctx)
				require.NoError(t, err)
				require.Equal(t, dispatch.Idle, outcome)
				require.Len(t, payloads, 1)
			}
		})
	}
}
func TestUpdateOutboxHoldsAndCorruptEnvelope(t *testing.T) {
	for _, mode := range []string{"sealed", "source revoked", "old epoch", "corrupt envelope", "migration hold"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			f, source := newUpdateFixture(t, ctx)
			switch mode {
			case "sealed":
				executionSQL(t, ctx, f.executionFixture, `UPDATE notification_runtime SET sealed=true`)
			case "source revoked":
				executionSQL(t, ctx, f.executionFixture, `UPDATE notification_log_sources SET active=false WHERE id=$1`, source.ID)
			case "old epoch":
				executionSQL(t, ctx, f.executionFixture, `UPDATE notification_runtime SET source_epoch=gen_random_uuid()`)
			case "migration hold":
				executionSQL(t, ctx, f.executionFixture, `UPDATE notification_runtime SET migration_hold=true`)
			case "corrupt envelope":
				executionSQL(t, ctx, f.executionFixture, `ALTER TABLE notification_update_outbox DISABLE TRIGGER guard_delivery_update_envelope; UPDATE notification_update_outbox SET payload='{}'::bytea; ALTER TABLE notification_update_outbox ENABLE TRIGGER guard_delivery_update_envelope`)
			}
			var calls int
			o := updateService(t, f, source, updatePublishFunc(func(context.Context, []byte) error { calls++; return nil }))
			outcome, err := o.DrainOne(ctx)
			require.NoError(t, err)
			want := dispatch.Held
			if mode == "old epoch" {
				want = dispatch.Idle
			}
			if mode == "migration hold" {
				want = dispatch.Published
				require.Equal(t, 1, calls)
			} else {
				require.Zero(t, calls)
			}
			require.Equal(t, want, outcome)
			_, _, count := f.state(t, ctx)
			if mode == "migration hold" {
				require.Zero(t, count)
			} else {
				require.Equal(t, 1, count)
			}
		})
	}
}

type verifiedUpdateFixture struct {
	producer *kafka.ScopedProducer
	source   dispatch.SourceManifest
}

func (p verifiedUpdateFixture) PublishScopedDelivery(ctx context.Context, value []byte) error {
	if err := p.producer.VerifyClusterTopic(ctx, p.source.ClusterID, p.source.PartitionCount); err != nil {
		return err
	}
	return p.producer.PublishScopedDelivery(ctx, value)
}
func TestUpdateOutboxPublishesExactBytesToKafka(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f, source := newUpdateFixture(t, ctx)
	_, original := f.event(t, ctx)
	transport := &broker.Transport{DialTimeout: time.Second}
	defer transport.CloseIdleConnections()
	client := &broker.Client{Addr: broker.TCP("127.0.0.1:19092"), Transport: transport, Timeout: time.Second}
	metadata, err := client.Metadata(ctx, &broker.MetadataRequest{Topics: []string{source.Topic}})
	require.NoError(t, err)
	source.ClusterID = metadata.ClusterID
	source.Incarnation = "actual-kafka-fixture"
	// Source identity is immutable; approve a separate reviewed fixture descriptor.
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active) VALUES(gen_random_uuid(),$1,$2,'actual-kafka-fixture',true) RETURNING id`, source.Topic, source.ClusterID).Scan(&source.ID))
	start := lastTopicOffset(t, ctx, source.Topic)
	producer := kafka.NewScopedProducer("127.0.0.1:19092", source.Topic)
	defer func() { require.NoError(t, producer.Close()) }()
	o := updateService(t, f, source, verifiedUpdateFixture{producer, source})
	outcome, err := o.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)
	reader := broker.NewReader(broker.ReaderConfig{Brokers: []string{"127.0.0.1:19092"}, Topic: source.Topic, Partition: 0, MinBytes: 1, MaxBytes: 65536, MaxWait: 100 * time.Millisecond})
	defer reader.Close()
	require.NoError(t, reader.SetOffset(start))
	message, err := reader.ReadMessage(ctx)
	require.NoError(t, err)
	require.Equal(t, kafka.DeliveryUpdateKey(f.campaign), message.Key)
	require.Equal(t, original, message.Value)
	_, _, count := f.state(t, ctx)
	require.Zero(t, count)
}

func TestUpdateOutboxForeignLineagePredecessorRequiresReconciliation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f, source := newUpdateFixture(t, ctx)
	next := replayUUID(t, ctx, f.pool)
	addNextUpdate(t, ctx, f, next)
	executionSQL(t, ctx, f.executionFixture, `UPDATE notification_runtime SET source_epoch=$1`, next)
	o := updateService(t, f, source, updatePublishFunc(func(context.Context, []byte) error { t.Error("unreconciled lineage published"); return nil }))
	outcome, err := o.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Held, outcome)
	_, _, count := f.state(t, ctx)
	require.Equal(t, 2, count)
}

func TestUpdateOutboxCancellationRetainsEvidenceAndReleasesLocks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f, source := newUpdateFixture(t, ctx)
	finished := make(chan struct{})
	o := updateService(t, f, source, updatePublishFunc(func(ctx context.Context, _ []byte) error { <-ctx.Done(); close(finished); return ctx.Err() }))
	call, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	started := time.Now()
	outcome, err := o.DrainOne(call)
	require.Error(t, err)
	require.Empty(t, outcome)
	require.Less(t, time.Since(started), time.Second)
	select {
	case <-finished:
	default:
		t.Fatal("publisher callback abandoned")
	}
	_, _, count := f.state(t, ctx)
	require.Equal(t, 1, count)
	check, release := context.WithTimeout(ctx, time.Second)
	defer release()
	_, err = f.pool.Exec(check, `UPDATE notification_update_outbox SET attempts=attempts`)
	require.NoError(t, err)
}

func TestUpdateOutboxUnknownScopeNeverFallsBackToAllApplications(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f, source := newUpdateFixture(t, ctx)
	other := seedOtherWorkerEvent(t, ctx, f.executionFixture)
	// Give the foreign event priority in global due order.
	executionSQL(t, ctx, f.executionFixture, "UPDATE notification_update_outbox SET next_attempt_at=statement_timestamp()-interval '1 hour' WHERE event_id=$1", other)
	var sent []string
	publisher := updatePublishFunc(func(_ context.Context, value []byte) error {
		e, err := kafka.DecodeScopedDelivery(value)
		if err == nil {
			sent = append(sent, e.ApplicationID)
		}
		return err
	})
	unknown, err := dispatch.NewApplicationUpdateOutbox(f.db, publisher, source, replayUUID(t, ctx, f.pool))
	require.NoError(t, err)
	scoped, err := dispatch.NewApplicationUpdateOutbox(f.db, publisher, source, f.project)
	require.NoError(t, err)
	global := updateService(t, f, source, publisher)
	outcome, err := unknown.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Idle, outcome)
	outcome, err = scoped.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)
	require.Equal(t, []string{f.project.String()}, sent)
	outcome, err = global.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)
	require.Len(t, sent, 2)
	require.NotEqual(t, sent[0], sent[1])
}
