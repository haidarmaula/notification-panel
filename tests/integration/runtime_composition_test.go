//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/features/applications"
	"hello/internal/installation"
	"hello/internal/kafka"
	"hello/internal/middleware"
	"hello/internal/runloop"
	"hello/internal/scheduler"
	"hello/internal/token"
)

func TestSourceOutboxHoldsInactiveRegistration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	event := f.acceptedEvent(t, ctx)
	_, err := f.pool.Exec(ctx, `INSERT INTO notification_send_outbox(application_id,notification_id,requested_at,deadline_at,origin_epoch,source_epoch)
  SELECT application_id,id,queued_at,deadline_at,origin_epoch,source_epoch FROM notifications WHERE id=$1`, f.campaign)
	require.NoError(t, err)
	calls := 0
	source := dispatch.SourceManifest{ID: f.source, ClusterID: "fixture", Topic: "send", Incarnation: "incarnation", PartitionCount: 1}
	outbox, err := dispatch.NewSourceOutbox(f.pool, scopedPublishFunc(func(_ context.Context, got kafka.ScopedSendRequested) error {
		calls++
		require.Equal(t, event, got)
		return nil
	}), source)
	require.NoError(t, err)
	source.Topic = "changed-after-construction"
	_, err = f.pool.Exec(ctx, "UPDATE notification_log_sources SET active=false WHERE id=$1", f.source)
	require.NoError(t, err)
	outcome, err := outbox.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Held, outcome)
	require.Zero(t, calls)
	var attempts int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT publish_attempts FROM notification_send_outbox").Scan(&attempts))
	require.Zero(t, attempts)
	_, err = f.pool.Exec(ctx, "UPDATE notification_log_sources SET active=true WHERE id=$1", f.source)
	require.NoError(t, err)
	outcome, err = outbox.DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, outcome)
	require.Equal(t, 1, calls)
}

func TestSchedulerAndAuthenticatedWorkerComposeDurablePipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	_, err := f.pool.Exec(ctx, `UPDATE notification_runtime SET migration_hold=false;
 INSERT INTO application_dispatch_policy(application_id,hold_new,coverage_approved,push_ttl_seconds,key_ttl_seconds)
 SELECT id,false,true,3600,86400 FROM applications`)
	require.NoError(t, err)
	var user int64
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO users(application_id,external_id) VALUES($1,'runtime-user') RETURNING id", f.app).Scan(&user))
	protector, err := installation.NewProtector(1, map[int32][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	lifecycle, err := installation.NewLifecycle(f.pool, protector)
	require.NoError(t, err)
	_, err = lifecycle.Enroll(ctx, installation.Principal{ApplicationID: f.app, UserID: user}, installation.Route{Namespace: "runtime-fixture", Key: "runtime-route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}, "runtime-fixture-address")
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, "INSERT INTO notification_targets(application_id,notification_id,target_type) VALUES($1,$2,'BROADCAST')", f.app, f.campaign)
	require.NoError(t, err)
	staffCtx := context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: f.staff})
	scheduled, err := applications.NewService(f.q, f.pool).ScheduleCampaign(staffCtx, f.project, f.campaign, time.Now().UTC().Add(time.Second))
	require.NoError(t, err)
	transport := &broker.Transport{DialTimeout: time.Second}
	defer transport.CloseIdleConnections()
	client := &broker.Client{Addr: broker.TCP("127.0.0.1:19092"), Timeout: 3 * time.Second, Transport: transport}
	topic := fmt.Sprintf("runtime-fixture-%d", time.Now().UnixNano())
	created, err := client.CreateTopics(ctx, &broker.CreateTopicsRequest{Topics: []broker.TopicConfig{{Topic: topic, NumPartitions: 2, ReplicationFactor: 1}}})
	require.NoError(t, err)
	require.NoError(t, created.Errors[topic])
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		deleted, err := client.DeleteTopics(cleanup, &broker.DeleteTopicsRequest{Topics: []string{topic}})
		require.NoError(t, err)
		require.NoError(t, deleted.Errors[topic])
	}()
	var metadata *broker.MetadataResponse
	require.Eventually(t, func() bool {
		var err error
		metadata, err = client.Metadata(ctx, &broker.MetadataRequest{Topics: []string{topic}})
		return err == nil && metadata != nil && kafka.ValidateScopedMetadata(metadata, metadata.ClusterID, topic, 2) == nil
	}, 5*time.Second, 100*time.Millisecond)
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active) VALUES(gen_random_uuid(),$1,$2,'runtime-generation',true) RETURNING id`, topic, metadata.ClusterID).Scan(&f.source))
	source := dispatch.SourceManifest{ID: f.source, ClusterID: metadata.ClusterID, Topic: topic, Incarnation: "runtime-generation", PartitionCount: 2}
	tlsConfig := admissionFixtureTLS(t)
	producer, err := kafka.NewAuthenticatedScopedProducer(kafka.ScopedProducerConfig{Brokers: []string{"127.0.0.1:19095"}, Topic: topic, TLS: tlsConfig})
	require.NoError(t, err)
	defer func() { require.NoError(t, producer.Close()) }()
	require.NoError(t, producer.VerifyClusterTopic(ctx, source.ClusterID, source.PartitionCount))
	publisher := scopedPublishFunc(func(ctx context.Context, event kafka.ScopedSendRequested) error {
		if err := producer.VerifyClusterTopic(ctx, source.ClusterID, source.PartitionCount); err != nil {
			return err
		}
		return producer.PublishScopedSend(ctx, event)
	})
	var runtimeLog bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&runtimeLog, nil))
	defer func() {
		if t.Failed() {
			t.Log(runtimeLog.String())
		}
	}()
	policy := runloop.RetryPolicy{MinimumDelay: 100 * time.Millisecond, MaximumDelay: time.Second}
	scheduledRuntime, err := scheduler.New(f.pool, publisher, source, scheduler.Config{Interval: 100 * time.Millisecond, PageSize: 10, Retry: policy}, logger)
	require.NoError(t, err)
	preparer, err := dispatch.NewPreparer(f.pool)
	require.NoError(t, err)
	quarantine, err := dispatch.NewQuarantine(f.pool)
	require.NoError(t, err)
	groupID := topic + "-group"
	worker, err := dispatch.NewAuthenticatedKafkaAdmission(f.q, dispatch.KafkaAdmissionConfig{Brokers: []string{"127.0.0.1:19095"}, GroupID: groupID, SourceID: f.source, ClusterID: source.ClusterID, Topic: topic, Incarnation: source.Incarnation, PartitionCount: source.PartitionCount, TLS: tlsConfig}, preparer, quarantine)
	require.NoError(t, err)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- runloop.RunGroup(runCtx, scheduledRuntime.Run, func(ctx context.Context) error {
			return runloop.Supervise(ctx, "admission", worker.Run, policy, logger)
		})
	}()
	defer func() {
		stop()
		select {
		case err := <-done:
			require.True(t, errors.Is(err, context.Canceled), "unexpected composed shutdown: %v", err)
		case <-time.After(8 * time.Second):
			t.Error("runtime tasks/fetchers failed to join")
		}
	}()
	require.Eventually(t, func() bool {
		var complete, intents, pending int
		if f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_preparations WHERE notification_id=$1", f.campaign).Scan(&complete) != nil {
			return false
		}
		if f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_delivery_intents WHERE notification_id=$1 AND attempt_count=0 AND delivery_version=0", f.campaign).Scan(&intents) != nil {
			return false
		}
		if f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_send_outbox").Scan(&pending) != nil {
			return false
		}
		if complete != 1 || intents != 1 || pending != 0 {
			return false
		}
		offsets, err := client.OffsetFetch(ctx, &broker.OffsetFetchRequest{GroupID: groupID, Topics: map[string][]int{topic: {0, 1}}})
		if err != nil || offsets.Error != nil || len(offsets.Topics[topic]) != 2 {
			return false
		}
		committed := 0
		for _, partition := range offsets.Topics[topic] {
			if partition.Error != nil {
				return false
			}
			if partition.CommittedOffset == 1 {
				committed++
			} else if partition.CommittedOffset > 1 {
				return false
			}
		}
		return committed == 1
	}, 20*time.Second, 100*time.Millisecond, "schedule -> outbox -> authenticated admission must finish with one untouched intent")
	var status string
	var deadline time.Time
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT status,deadline_at FROM notifications WHERE id=$1", f.campaign).Scan(&status, &deadline))
	require.Equal(t, "PROCESSING", status)
	require.True(t, scheduled.DeadlineAt.Time.Equal(deadline), "polling cannot extend accepted TTL")
}
