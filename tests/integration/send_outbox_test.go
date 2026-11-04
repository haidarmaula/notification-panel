//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/kafka"
)

type scopedPublishFunc func(context.Context, kafka.ScopedSendRequested) error

func (f scopedPublishFunc) PublishScopedSend(ctx context.Context, e kafka.ScopedSendRequested) error {
	return f(ctx, e)
}

type failOutboxCommit struct{ dispatch.Transactor }
type failedOutboxTx struct{ pgx.Tx }

func (f failOutboxCommit) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	tx, err := f.Transactor.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return failedOutboxTx{tx}, nil
}

func (tx failedOutboxTx) Commit(ctx context.Context) error {
	_ = tx.Tx.Rollback(ctx) // Crash window: broker ack, no database commit.
	return errors.New("injected commit failure")
}

func TestDurableSendOutbox(t *testing.T) {
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
	appID := count("INSERT INTO applications(key,name) VALUES('outbox','Outbox fixture') RETURNING id")
	staffID := count("INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Outbox','outbox@fixture.invalid','fixture' FROM roles WHERE name='ADMIN' RETURNING id")
	seed := func() int64 {
		t.Helper()
		id := count("INSERT INTO notifications(application_id,title,body,created_by,status,queued_at,deadline_at,origin_epoch,source_epoch) SELECT $1,'Outbox','Body',$2,'QUEUED',clock_timestamp(),clock_timestamp()+interval '1 hour',origin_epoch,source_epoch FROM notification_runtime RETURNING id", appID, staffID)
		exec("INSERT INTO notification_send_outbox(application_id,notification_id,requested_at,deadline_at,origin_epoch,source_epoch) SELECT application_id,id,queued_at,deadline_at,origin_epoch,source_epoch FROM notifications WHERE id=$1", id)
		return id
	}
	makeDrain := func(db dispatch.Transactor, publish dispatch.Publisher) *dispatch.Outbox {
		t.Helper()
		d, err := dispatch.NewOutbox(db, publish)
		require.NoError(t, err)
		return d
	}
	var events []kafka.ScopedSendRequested
	record := scopedPublishFunc(func(_ context.Context, event kafka.ScopedSendRequested) error {
		events = append(events, event)
		return nil
	})
	drain := makeDrain(pool, record)
	id := seed()
	outcome, err := drain.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Held, outcome)
	require.Empty(t, events)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notification_send_outbox"))
	// Migration hold does not strand already accepted work.
	exec("UPDATE notification_runtime SET sealed=false")
	outcome, err = drain.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)
	require.EqualValues(t, id, events[0].NotificationID)
	require.NoError(t, events[0].Validate())
	require.Zero(t, count("SELECT count(*) FROM notification_send_outbox"))
	outcome, err = drain.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Idle, outcome)

	id = seed()
	failure := makeDrain(pool, scopedPublishFunc(func(context.Context, kafka.ScopedSendRequested) error {
		return errors.New("sensitive provider detail must never persist")
	}))
	outcome, err = failure.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Deferred, outcome)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notification_send_outbox WHERE notification_id=$1 AND publish_attempts=1 AND next_publish_at>clock_timestamp() AND last_error='broker_publish_failed'", id))
	outcome, err = drain.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Idle, outcome)
	// Prior lineage never acquires the current epoch implicitly.
	exec("UPDATE notification_send_outbox SET next_publish_at=clock_timestamp(),source_epoch=gen_random_uuid()")
	outcome, err = drain.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Idle, outcome)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notification_send_outbox"))
	exec("UPDATE notification_send_outbox SET source_epoch=(SELECT source_epoch FROM notification_runtime)")

	crashDrain := makeDrain(failOutboxCommit{pool}, record)
	before := len(events)
	outcome, err = crashDrain.DrainOne(ctx)
	require.ErrorContains(t, err, "injected commit failure")
	require.Empty(t, outcome)
	require.Len(t, events, before+1, "no automatic transaction retry after publish")
	require.EqualValues(t, 1, count("SELECT count(*) FROM notification_send_outbox"))
	outcome, err = drain.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)
	require.Equal(t, events[before], events[before+1], "ack-before-commit recovery republishes identical identity")

	// Two dispatchers cannot claim the same locked row, nor lock the whole batch.
	firstID, secondID := seed(), seed()
	entered := make(chan kafka.ScopedSendRequested, 1)
	release := make(chan struct{})
	defer close(release)
	first := makeDrain(pool, scopedPublishFunc(func(ctx context.Context, e kafka.ScopedSendRequested) error {
		entered <- e
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	done := make(chan error, 1)
	go func() { _, err := first.DrainOne(ctx); done <- err }()
	select {
	case e := <-entered:
		require.Equal(t, firstID, e.NotificationID)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	outcome, err = drain.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)
	require.Equal(t, secondID, events[len(events)-1].NotificationID)
	// Let its bounded publisher finish; cancellation is persisted as a retry.
	require.NoError(t, <-done)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notification_send_outbox WHERE notification_id=$1 AND publish_attempts=1", firstID))
	exec("UPDATE notification_send_outbox SET next_publish_at=clock_timestamp()")
	outcome, err = drain.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)

	// Exercise the real modern producer against the isolated local Kafka broker.
	id = seed()
	topic := "notification.send.requested"
	start := lastTopicOffset(t, ctx, topic)
	producer := kafka.NewScopedProducer("127.0.0.1:19092", topic)
	defer producer.Close()
	outcome, err = makeDrain(pool, producer).DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)
	reader := broker.NewReader(broker.ReaderConfig{Brokers: []string{"127.0.0.1:19092"}, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1_000_000, MaxWait: 100 * time.Millisecond})
	defer reader.Close()
	require.NoError(t, reader.SetOffset(start))
	message, err := reader.ReadMessage(ctx)
	require.NoError(t, err)
	require.Equal(t, "notification_"+strconv.FormatInt(id, 10), string(message.Key))
	var envelope kafka.ScopedSendRequested
	require.NoError(t, json.Unmarshal(message.Value, &envelope))
	require.NoError(t, envelope.Validate())
	require.Equal(t, id, envelope.NotificationID)
	require.Zero(t, count("SELECT count(*) FROM notification_send_outbox"))

	// A real refused broker connection also retains work for a future poll.
	id = seed()
	refused := kafka.NewScopedProducer("127.0.0.1:1", topic)
	defer refused.Close()
	outcome, err = makeDrain(pool, refused).DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Deferred, outcome)
	require.EqualValues(t, 1, count("SELECT count(*) FROM notification_send_outbox WHERE notification_id=$1 AND publish_attempts=1", id))
}
