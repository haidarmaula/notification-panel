//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
)

func TestSourceAwareQuarantine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := acceptanceFixture(t, ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	count := func() int64 {
		t.Helper()
		var n int64
		require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM notification_send_dead_letters").Scan(&n))
		return n
	}
	var source pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation) VALUES(gen_random_uuid(),'send','fixture-cluster','generation-a') RETURNING id").Scan(&source))
	store, err := dispatch.NewQuarantine(pool)
	require.NoError(t, err)
	message := dispatch.SourceMessage{LogSourceID: source, Topic: "send", Partition: 0, Offset: 42}
	require.ErrorIs(t, store.Store(ctx, message, dispatch.InvalidEnvelope, 1), dispatch.ErrQuarantineSource)
	exec("UPDATE notification_runtime SET sealed=false")
	require.ErrorIs(t, store.Store(ctx, message, dispatch.InvalidEnvelope, 1), dispatch.ErrQuarantineSource)
	exec("UPDATE notification_log_sources SET active=true")
	require.NoError(t, store.Store(ctx, message, dispatch.InvalidEnvelope, 1), "unkeyed/empty messages are still evidence")
	require.NoError(t, store.Store(ctx, message, dispatch.InvalidEnvelope, 1))
	require.EqualValues(t, 1, count())
	message.Key, message.Payload = []byte{0xff, 0x00}, []byte("sensitive malformed bytes")
	require.ErrorIs(t, store.Store(ctx, message, dispatch.InvalidEnvelope, 1), dispatch.ErrQuarantineSource)
	message.Offset++
	require.NoError(t, store.Store(ctx, message, dispatch.InvalidEnvelope, 1))
	var key, value []byte
	require.NoError(t, pool.QueryRow(ctx, "SELECT original_key,original_payload FROM notification_send_dead_letters WHERE original_offset=$1", message.Offset).Scan(&key, &value))
	require.Equal(t, message.Key, key)
	require.Equal(t, message.Payload, value)
	require.EqualValues(t, 2, count())
	message.Topic = "wrong-topic"
	require.ErrorIs(t, store.Store(ctx, message, dispatch.InvalidEnvelope, 1), dispatch.ErrQuarantineSource)
	message.Topic = "send"
	require.ErrorIs(t, store.Store(ctx, message, dispatch.QuarantineReason("raw provider error"), 1), dispatch.ErrQuarantineInput)
	require.ErrorIs(t, store.Store(ctx, message, dispatch.InvalidEnvelope, 0), dispatch.ErrQuarantineInput)
	oversized := message
	oversized.Payload = make([]byte, (1<<20)+1)
	require.ErrorIs(t, store.Store(ctx, oversized, dispatch.InvalidEnvelope, 1), dispatch.ErrQuarantineInput)
	_, err = pool.Exec(ctx, "UPDATE notification_log_sources SET topic='mutated' WHERE id=$1", source)
	require.Error(t, err, "physical log identity is immutable")
	// Topic recreation has a new explicit identity even if offsets are reused.
	var recreated pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active) VALUES(gen_random_uuid(),'send','fixture-cluster','generation-b',true) RETURNING id").Scan(&recreated))
	message.LogSourceID = recreated
	require.NoError(t, store.Store(ctx, message, dispatch.InvalidEnvelope, 1))
	require.EqualValues(t, 3, count())
	message.Offset++
	failed, err := dispatch.NewQuarantine(failOutboxCommit{pool})
	require.NoError(t, err)
	require.ErrorContains(t, failed.Store(ctx, message, dispatch.AdmissionExhausted, 3), "injected commit failure")
	require.EqualValues(t, 3, count())
	require.NoError(t, store.Store(ctx, message, dispatch.AdmissionExhausted, 3))
	require.EqualValues(t, 4, count())
	exec("ALTER TABLE notification_send_dead_letters ADD CONSTRAINT injected_quarantine_failure CHECK(original_offset<45)")
	message.Offset = 99
	require.Error(t, store.Store(ctx, message, dispatch.InvalidEnvelope, 1), "failed evidence is unsafe for offset commit")
	require.EqualValues(t, 4, count())
}
