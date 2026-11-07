//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"testing"
	"time"

	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/kafka"
)

// Provision a stopped fixture group explicitly. Startup never installs offsets.
func seedAdmissionOffsets(t *testing.T, ctx context.Context, client *broker.Client, group, topic string, offsets ...int64) {
	t.Helper()
	partitions := make([]broker.OffsetCommit, len(offsets))
	for i, offset := range offsets {
		partitions[i] = broker.OffsetCommit{Partition: i, Offset: offset}
	}
	require.Eventually(t, func() bool {
		response, err := client.OffsetCommit(ctx, &broker.OffsetCommitRequest{
			GroupID: group, GenerationID: -1, Topics: map[string][]broker.OffsetCommit{topic: partitions},
		})
		if err != nil || len(response.Topics[topic]) != len(partitions) {
			return false
		}
		for _, part := range response.Topics[topic] {
			if part.Error != nil {
				return false
			}
		}
		return true
	}, 10*time.Second, 100*time.Millisecond, "fixture coordinator must acknowledge explicit offsets")
}

func TestAdmissionRefusesMissingExpiredAndFutureCheckpoints(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 65*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	topic := fmt.Sprintf("checkpoint-fixture-%d", time.Now().UnixNano())
	transport := &broker.Transport{DialTimeout: time.Second}
	defer transport.CloseIdleConnections()
	client := &broker.Client{Addr: broker.TCP("127.0.0.1:19092"), Timeout: 3 * time.Second, Transport: transport}
	created, err := client.CreateTopics(ctx, &broker.CreateTopicsRequest{Topics: []broker.TopicConfig{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}}})
	require.NoError(t, err)
	require.NoError(t, created.Errors[topic])
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		deleted, err := client.DeleteTopics(cleanup, &broker.DeleteTopicsRequest{Topics: []string{topic}})
		require.NoError(t, err)
		require.NoError(t, deleted.Errors[topic])
		transport.CloseIdleConnections()
	})
	metadata, err := client.Metadata(ctx, &broker.MetadataRequest{Topics: []string{topic}})
	require.NoError(t, err)
	cfg := dispatch.KafkaAdmissionConfig{SourceID: f.source, ClusterID: metadata.ClusterID, Incarnation: "checkpoint-fixture", Topic: topic,
		PartitionCount: 1, Brokers: []string{"127.0.0.1:19095"}, TLS: admissionFixtureTLS(t)}
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active)
		VALUES(gen_random_uuid(),$1,$2,$3,true) RETURNING id`, topic, cfg.ClusterID, cfg.Incarnation).Scan(&cfg.SourceID))
	e := f.acceptedEvent(t, ctx)
	w := kafka.NewScopedProducer("127.0.0.1:19092", topic)
	defer func() { require.NoError(t, w.Close()) }()
	require.NoError(t, w.PublishScopedSend(ctx, e))
	require.NoError(t, w.PublishScopedSend(ctx, e))
	// Advance the low watermark to one while retaining the second event.
	advanceFloor := func(offset int64) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"partitions": []any{map[string]any{"topic": topic, "partition": 0, "offset": offset}}, "version": 1})
		require.NoError(t, err)
		args := []string{"compose", "--env-file", "/dev/null", "-f", "../../compose.integration.yml", "-p", "hello-integration", "exec", "-T", "kafka",
			"/opt/kafka/bin/kafka-delete-records.sh", "--bootstrap-server", "kafka:9094", "--offset-json-file", "/dev/stdin"}
		command := exec.CommandContext(ctx, "docker", args...)
		command.Stdin = bytes.NewReader(body)
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
	}
	advanceFloor(1)
	quarantine, err := dispatch.NewQuarantine(f.pool)
	require.NoError(t, err)
	preparer, err := dispatch.NewPreparer(f.pool)
	require.NoError(t, err)
	for _, tc := range []struct {
		name       string
		checkpoint int64
	}{{"missing", -1}, {"expired", 0}, {"past-end", 3}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg.GroupID = topic + "-" + tc.name
			if tc.checkpoint >= 0 {
				seedAdmissionOffsets(t, ctx, client, cfg.GroupID, topic, tc.checkpoint)
			}
			admission, err := dispatch.NewAuthenticatedKafkaAdmission(f.q, cfg, preparer, quarantine)
			require.NoError(t, err)
			require.ErrorIs(t, admission.Run(ctx), dispatch.ErrAdmissionCheckpoint)
			offsets, err := client.OffsetFetch(ctx, &broker.OffsetFetchRequest{GroupID: cfg.GroupID, Topics: map[string][]int{topic: {0}}})
			require.NoError(t, err)
			require.NoError(t, offsets.Error)
			require.Len(t, offsets.Topics[topic], 1)
			require.NoError(t, offsets.Topics[topic][0].Error)
			require.Equal(t, tc.checkpoint, offsets.Topics[topic][0].CommittedOffset)
			var mutations int
			require.NoError(t, f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM notification_preparations)
				+(SELECT count(*) FROM notification_send_dead_letters)`).Scan(&mutations))
			require.Zero(t, mutations)
		})
	}

	// A healthy existing group is blocked processing retained offset one. Fewer
	// than eight half-megabyte records fit its bounded fetch, merge channel and
	// in-process admission. Deleting them forces a gap before the final canary.
	cfg.GroupID = topic + "-running"
	seedAdmissionOffsets(t, ctx, client, cfg.GroupID, topic, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	blocked := brokerPreparationFixture(func(ctx context.Context, event kafka.ScopedSendRequested) (dispatch.PreparationResult, error) {
		select {
		case <-entered:
			t.Error("processed a later canary across a retention gap")
		default:
			close(entered)
		}
		select {
		case <-release:
			return preparer.Prepare(ctx, event)
		case <-ctx.Done():
			return dispatch.PreparationResult{}, ctx.Err()
		}
	})
	admission, err := dispatch.NewAuthenticatedKafkaAdmission(f.q, cfg, blocked, quarantine)
	require.NoError(t, err)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	var runErr error
	go func() { defer close(done); runErr = admission.Run(runCtx) }()
	defer func() {
		stop()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("generation did not join")
		}
	}()
	select {
	case <-entered:
	case <-done:
		t.Fatalf("admission stopped before canary: %v", runErr)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	writer := &broker.Writer{Addr: client.Addr, Topic: topic, BatchSize: 1, RequiredAcks: broker.RequireAll, Transport: transport}
	defer func() { require.NoError(t, writer.Close()) }()
	poison := bytes.Repeat([]byte{0xab}, 512<<10)
	for range 8 {
		require.NoError(t, writer.WriteMessages(ctx, broker.Message{Value: poison}))
	}
	require.NoError(t, w.PublishScopedSend(ctx, e))
	last := lastTopicOffset(t, ctx, topic)
	advanceFloor(last - 1)
	close(release)
	select {
	case <-done:
		require.ErrorIs(t, runErr, dispatch.ErrAdmissionCheckpoint)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	result, err := client.OffsetFetch(ctx, &broker.OffsetFetchRequest{GroupID: cfg.GroupID, Topics: map[string][]int{topic: {0}}})
	require.NoError(t, err)
	require.NoError(t, result.Error)
	require.Len(t, result.Topics[topic], 1)
	checkpoint := result.Topics[topic][0]
	require.NoError(t, checkpoint.Error)
	require.GreaterOrEqual(t, checkpoint.CommittedOffset, int64(2))
	require.Less(t, checkpoint.CommittedOffset, last-1)

}
