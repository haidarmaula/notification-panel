//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
	"hello/internal/dispatch"
	"hello/internal/installation"
	"hello/internal/kafka"
)

type brokerPreparationFixture func(context.Context, kafka.ScopedSendRequested) (dispatch.PreparationResult, error)

func (f brokerPreparationFixture) Prepare(ctx context.Context, e kafka.ScopedSendRequested) (dispatch.PreparationResult, error) {
	return f(ctx, e)
}

// Real group assignments/commits and DB preparation with one injected suspension
// before preparation, to verify redelivery and immutable intent dedup together.
func TestKafkaAdmissionGenerationHandoff(t *testing.T) {
	kafkaAdmissionGenerationHandoff(t, false)
}

func TestAuthenticatedKafkaAdmissionGenerationHandoff(t *testing.T) {
	kafkaAdmissionGenerationHandoff(t, true)
}

func kafkaAdmissionGenerationHandoff(t *testing.T, authenticated bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	pool := acceptanceFixture(t, ctx)
	transport := &broker.Transport{DialTimeout: time.Second}
	defer transport.CloseIdleConnections()
	client := &broker.Client{Addr: broker.TCP("127.0.0.1:19092"), Timeout: 3 * time.Second, Transport: transport}
	topic := fmt.Sprintf("admission-fixture-%d", time.Now().UnixNano())
	created, err := client.CreateTopics(ctx, &broker.CreateTopicsRequest{Topics: []broker.TopicConfig{{Topic: topic, NumPartitions: 2, ReplicationFactor: 1}}})
	require.NoError(t, err)
	require.NoError(t, created.Errors[topic])
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		deleted, err := client.DeleteTopics(cleanup, &broker.DeleteTopicsRequest{Topics: []string{topic}})
		require.NoError(t, err)
		require.NoError(t, deleted.Errors[topic])
	}()
	var metadata *broker.MetadataResponse
	require.Eventually(t, func() bool {
		var err error
		metadata, err = client.Metadata(ctx, &broker.MetadataRequest{Topics: []string{topic}})
		if err != nil || len(metadata.Topics) != 1 || metadata.Topics[0].Error != nil || len(metadata.Topics[0].Partitions) != 2 {
			return false
		}
		for _, partition := range metadata.Topics[0].Partitions {
			if partition.Error != nil || partition.Leader.Host == "" || partition.Leader.ID < 0 {
				return false
			}
		}
		return true
	}, 5*time.Second, 100*time.Millisecond)
	require.NotEmpty(t, metadata.ClusterID)
	var source pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active) VALUES(gen_random_uuid(),$1,$2,'fixture-generation',true) RETURNING id", topic, metadata.ClusterID).Scan(&source))
	_, err = pool.Exec(ctx, "UPDATE notification_runtime SET sealed=false,migration_hold=false")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "ALTER TABLE applications DROP CONSTRAINT applications_status_check")
	require.NoError(t, err)
	var appID, userID, staffID int64
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO applications(key,name,status) VALUES('broker-admission','Broker admission','ACTIVE') RETURNING id").Scan(&appID))
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO users(application_id,external_id) VALUES($1,'broker-admission-user') RETURNING id", appID).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Broker','broker@fixture.invalid','fixture' FROM roles WHERE name='ADMIN' RETURNING id").Scan(&staffID))
	_, err = pool.Exec(ctx, "INSERT INTO application_dispatch_policy(application_id,hold_new,coverage_approved,push_ttl_seconds,key_ttl_seconds) VALUES($1,false,true,3600,86400)", appID)
	require.NoError(t, err)
	protector, err := installation.NewProtector(1, map[int32][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	lifecycle, err := installation.NewLifecycle(pool, protector)
	require.NoError(t, err)
	_, err = lifecycle.Enroll(ctx, installation.Principal{ApplicationID: appID, UserID: userID}, installation.Route{Namespace: "broker-fixture", Key: "broker-route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}, "broker-fixture-address")
	require.NoError(t, err)
	actualPreparer, err := dispatch.NewPreparer(pool)
	require.NoError(t, err)
	quarantine, err := dispatch.NewQuarantine(pool)
	require.NoError(t, err)
	config := dispatch.KafkaAdmissionConfig{Brokers: []string{"127.0.0.1:19092"}, GroupID: topic + "-group", SourceID: source, Topic: topic, ClusterID: metadata.ClusterID, Incarnation: "fixture-generation"}
	if authenticated {
		config.Brokers = []string{"127.0.0.1:19095"}
		config.PartitionCount = 2
		config.TLS = admissionFixtureTLS(t)
	}

	seedAdmissionOffsets(t, ctx, client, config.GroupID, topic, 0, 0)
	// Choose one key per partition using exactly the production hash balancer.
	ids := map[int]int64{}
	hash := &broker.Hash{}
	for id := int64(1); len(ids) < 2; id++ {
		partition := hash.Balance(broker.Message{Key: []byte("notification_" + strconv.FormatInt(id, 10))}, 0, 1)
		ids[partition] = id
	}
	producer := kafka.NewScopedProducer("127.0.0.1:19092", topic)
	defer producer.Close()
	for _, id := range ids {
		_, err := pool.Exec(ctx, "INSERT INTO notifications(id,application_id,title,body,created_by,status,queued_at,deadline_at,origin_epoch,source_epoch) SELECT $1,$2,'Broker','Body',$3,'QUEUED',clock_timestamp(),clock_timestamp()+interval '1 hour',origin_epoch,source_epoch FROM notification_runtime", id, appID, staffID)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, "INSERT INTO notification_targets(application_id,notification_id,target_type) VALUES($1,$2,'BROADCAST')", appID, id)
		require.NoError(t, err)
		event := kafka.ScopedSendRequested{SchemaVersion: 1, NotificationID: id}
		require.NoError(t, pool.QueryRow(ctx, "SELECT a.public_id::text,n.queued_at,n.deadline_at,n.origin_epoch::text,n.source_epoch::text FROM notifications n JOIN applications a ON a.id=n.application_id WHERE n.id=$1", id).Scan(&event.ApplicationID, &event.RequestedAt, &event.DeadlineAt, &event.OriginEpoch, &event.SourceEpoch))
		require.NoError(t, producer.PublishScopedSend(ctx, event))
	}
	poisonKey := []byte{0xff}
	poisonPartition := hash.Balance(broker.Message{Key: poisonKey}, 0, 1)
	rawTransport := &broker.Transport{DialTimeout: time.Second}
	defer rawTransport.CloseIdleConnections()
	writer := &broker.Writer{Addr: broker.TCP("127.0.0.1:19092"), Topic: topic, Balancer: &broker.Hash{}, BatchSize: 1, RequiredAcks: broker.RequireAll, MaxAttempts: 1, Transport: rawTransport}
	defer writer.Close()
	require.NoError(t, writer.WriteMessages(ctx, broker.Message{Key: poisonKey, Value: []byte("malformed")}))
	entered := make(chan int64, 1)
	revoked := make(chan struct{})
	prepared := make(chan int64, 16)
	var blockFirst atomic.Bool
	firstPreparer := brokerPreparationFixture(func(ctx context.Context, e kafka.ScopedSendRequested) (dispatch.PreparationResult, error) {
		if blockFirst.CompareAndSwap(false, true) {
			entered <- e.NotificationID
			<-ctx.Done()
			close(revoked)
			return dispatch.PreparationResult{}, ctx.Err()
		}
		result, err := actualPreparer.Prepare(ctx, e)
		if err != nil {
			return result, err
		}
		select {
		case prepared <- e.NotificationID:
		case <-ctx.Done():
			return dispatch.PreparationResult{}, ctx.Err()
		}
		return result, nil
	})
	secondPreparer := brokerPreparationFixture(func(ctx context.Context, e kafka.ScopedSendRequested) (dispatch.PreparationResult, error) {
		result, err := actualPreparer.Prepare(ctx, e)
		if err != nil {
			return result, err
		}
		select {
		case prepared <- e.NotificationID:
		case <-ctx.Done():
			return dispatch.PreparationResult{}, ctx.Err()
		}
		return result, nil
	})
	start := func(preparer dispatch.AdmissionPreparer) (context.CancelFunc, <-chan error) {
		t.Helper()
		var runner *dispatch.KafkaAdmission
		var err error
		if authenticated {
			runner, err = dispatch.NewAuthenticatedKafkaAdmission(sqlc.New(pool), config, preparer, quarantine)
		} else {
			runner, err = dispatch.NewKafkaAdmission(sqlc.New(pool), config, preparer, quarantine)
		}
		require.NoError(t, err)
		runCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- runner.Run(runCtx); close(done) }()
		return stop, done
	}
	stopFirst, firstDone := start(firstPreparer)
	join := func(done <-chan error) {
		t.Helper()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("admission run exit: %v", err)
			}
		case <-time.After(8 * time.Second):
			t.Error("generation fetchers did not join on shutdown")
		}
	}
	defer func() { stopFirst(); join(firstDone) }()
	var blocked int64
	select {
	case blocked = <-entered:
	case err := <-firstDone:
		t.Fatalf("first consumer stopped before preparation: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// While preparation is incomplete the source partition has no safe commit.
	offsets, err := client.OffsetFetch(ctx, &broker.OffsetFetchRequest{GroupID: config.GroupID, Topics: map[string][]int{topic: {0, 1}}})
	require.NoError(t, err)
	require.NoError(t, offsets.Error)
	require.Len(t, offsets.Topics[topic], 2)
	for partition, id := range ids {
		if id == blocked {
			for _, entry := range offsets.Topics[topic] {
				if entry.Partition == partition {
					require.LessOrEqual(t, entry.CommittedOffset, int64(0))
				}
			}
		}
	}
	stopSecond, secondDone := start(secondPreparer)
	defer func() { stopSecond(); join(secondDone) }()
	select {
	case <-revoked:
	case <-ctx.Done():
		t.Fatal("rebalance did not cancel unfinished preparation")
	}
	seen := map[int64]bool{}
	require.Eventually(t, func() bool {
		for len(prepared) > 0 {
			seen[<-prepared] = true
		}
		if len(seen) != 2 {
			return false
		}
		offsets, err := client.OffsetFetch(ctx, &broker.OffsetFetchRequest{GroupID: config.GroupID, Topics: map[string][]int{topic: {0, 1}}})
		if err != nil || offsets.Error != nil || len(offsets.Topics[topic]) != 2 {
			return false
		}
		for _, entry := range offsets.Topics[topic] {
			want := int64(1)
			if entry.Partition == poisonPartition {
				want++
			}
			if entry.Error != nil || entry.CommittedOffset != want {
				return false
			}
		}
		return true
	}, 20*time.Second, 100*time.Millisecond)
	var evidence int64
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM notification_send_dead_letters WHERE log_source_id=$1 AND original_key=$2", source, poisonKey).Scan(&evidence))
	require.EqualValues(t, 1, evidence)
	var intents, snapshots int64
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM notification_delivery_intents").Scan(&intents))
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM notification_preparations").Scan(&snapshots))
	require.EqualValues(t, 2, intents, "redelivery does not duplicate per-binding intent")
	require.EqualValues(t, 2, snapshots, "both accepted campaigns have complete snapshots before offset advance")
	stopFirst()
	stopSecond()
}

func admissionFixtureTLS(t *testing.T) *tls.Config {
	t.Helper()
	directory := os.Getenv("HELLO_INTEGRATION_CERTS_DIR")
	require.NotEmpty(t, directory, "use the isolated TLS fixture runner")
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join(directory, name))
		require.NoError(t, err)
		return data
	}
	certificate, err := tls.X509KeyPair(read("client.crt"), read("client.key"))
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(read("ca.crt")))
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{certificate}}
}
