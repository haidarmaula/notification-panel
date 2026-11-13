//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"hello/internal/daemon"
	"hello/internal/deployment"
	"hello/internal/dispatch"
	"hello/internal/features/applications"
	"hello/internal/installation"
	"hello/internal/kafka"
	"hello/internal/runloop"
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
		// Native database timestamps use the same canonical form as decoded wire events.
		got.RequestedAt, got.DeadlineAt = got.RequestedAt.UTC(), got.DeadlineAt.UTC()
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
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		if strings.HasPrefix(name, "PG") || name == "SSL_CERT_FILE" || name == "SSL_CERT_DIR" {
			t.Setenv(name, "")
		}
	}
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
	approveFixtureBindings(t, ctx, f.pool, f.app)
	_, err = f.pool.Exec(ctx, "INSERT INTO notification_targets(application_id,notification_id,target_type) VALUES($1,$2,'BROADCAST')", f.app, f.campaign)
	require.NoError(t, err)
	staffCtx := fixtureStaffContext(t, ctx, f.q, f.staff)
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
	// A server-side role selects the disposable schema; production policy has
	// no PGOPTIONS/search_path override. Fixture superuser is not a privilege model.
	role := fmt.Sprintf("daemon_role_%d", time.Now().UnixNano())
	roleSQL := pgx.Identifier{role}.Sanitize()
	_, err = f.pool.Exec(ctx, "CREATE ROLE "+roleSQL+" LOGIN SUPERUSER PASSWORD 'integration_only'")
	require.NoError(t, err)
	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	_, err = f.pool.Exec(ctx, "ALTER ROLE "+roleSQL+" SET search_path TO "+pgx.Identifier{schema}.Sanitize()+",pg_catalog")
	require.NoError(t, err)
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		var sessions int
		require.NoError(t, f.pool.QueryRow(cleanup, "SELECT count(*) FROM pg_stat_activity WHERE usename=$1", role).Scan(&sessions))
		require.Zero(t, sessions, "daemon pools must close before role cleanup")
		_, err := f.pool.Exec(cleanup, "DROP ROLE "+roleSQL)
		require.NoError(t, err)
	}()
	directory := os.Getenv("HELLO_INTEGRATION_CERTS_DIR")
	require.NotEmpty(t, directory)
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(directory, name))
		require.NoError(t, err)
		return string(data)
	}
	uri := "postgresql://" + role + ":integration_only@127.0.0.1:15432/notification_integration?sslmode=verify-full&connect_timeout=3&sslrootcert=" + url.QueryEscape(filepath.Join(directory, "ca.crt"))
	groupID := topic + "-group"
	seedAdmissionOffsets(t, ctx, client, groupID, topic, 0, 0)
	load := func(role deployment.Component, change func(map[string]any)) *daemon.Configuration {
		policy := map[string]any{
			"database_url": uri, "log_source_id": source.ID.String(), "cluster_identity": source.ClusterID, "topic_name": source.Topic, "topic_incarnation": source.Incarnation, "partition_count": 2,
			"retry_min_ms": 100, "retry_max_ms": 1000,
			"kafka": map[string]any{"brokers": []string{"127.0.0.1:19095"}, "authentication": "MTLS", "ca_pem": read("ca.crt"), "client_certificate_pem": read("client.crt"), "client_key_pem": read("client.key"), "sasl_username": "", "sasl_password": ""},
		}
		if role == deployment.Worker {
			policy["mode"] = "admission_only"
			policy["group_id"] = groupID
		} else {
			policy["poll_interval_ms"] = 100
			policy["page_size"] = 10
		}
		if change != nil {
			change(policy)
		}
		data, err := json.Marshal(policy)
		require.NoError(t, err)
		cfg, err := daemon.Load(role, bytes.NewReader(data))
		require.NoError(t, err)
		return cfg
	}
	// Startup rejection must join/close its pool and preserve the accepted work.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	for _, change := range []func(map[string]any){
		func(p map[string]any) { p["topic_incarnation"] = "wrong-generation" },
		func(p map[string]any) { p["partition_count"] = 3 },
		func(p map[string]any) { p["kafka"].(map[string]any)["ca_pem"] = "" },
		func(p map[string]any) { p["database_url"] = strings.Split(uri, "&sslrootcert=")[0] },
	} {
		require.Error(t, load(deployment.Worker, change).Run(ctx, logger))
	}
	scheduledRuntime := load(deployment.Scheduler, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	diagnostics := listener.Addr().String()
	require.NoError(t, listener.Close())
	worker := load(deployment.Worker, func(p map[string]any) {
		p["monitoring"] = map[string]string{
			"listen": diagnostics, "client_ca_pem": read("ca.crt"),
			"server_certificate_pem": read("server.crt"), "server_key_pem": read("server.key"),
		}
	})
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- runloop.RunGroup(runCtx, func(ctx context.Context) error { return scheduledRuntime.Run(ctx, logger) }, func(ctx context.Context) error { return worker.Run(ctx, logger) })
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

	scraperTransport := &http.Transport{TLSClientConfig: admissionFixtureTLS(t)}
	defer scraperTransport.CloseIdleConnections()
	scraper := &http.Client{Transport: scraperTransport, Timeout: time.Second}
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+diagnostics+"/metrics", nil)
		if err != nil {
			return false
		}
		res, err := scraper.Do(req)
		if err != nil {
			return false
		}
		defer res.Body.Close()
		body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		return err == nil && res.StatusCode == http.StatusOK &&
			strings.Contains(string(body), "hello_kafka_group_sample_healthy 1") &&
			strings.Contains(string(body), `hello_kafka_group_lag_offsets{partition="0"} 0`) &&
			strings.Contains(string(body), `hello_kafka_group_lag_offsets{partition="1"} 0`)
	}, 16*time.Second, 100*time.Millisecond, "the monitored admission worker must expose its committed Kafka offsets")
}
