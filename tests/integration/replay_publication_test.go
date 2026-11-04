//go:build integration

package integration

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/kafka"
)

func TestReplayPublicationAndResultAreIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	event := f.acceptedEvent(t, ctx)
	f.quarantineEvent(t, ctx, event)
	request := f.input(t, ctx)
	credential, _ := f.credential(t, ctx, request)
	claim, err := f.service.Claim(ctx, request, credential, request.Positions[0])
	require.NoError(t, err)
	var calls atomic.Int32
	executor, err := dispatch.NewReplayExecutor(f.service, scopedPublishFunc(func(ctx context.Context, got kafka.ScopedSendRequested) error {
		calls.Add(1)
		require.Equal(t, event, got, "replay cannot restamp epochs or deadlines")
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 2*time.Second)
		return nil
	}))
	require.NoError(t, err)
	result, err := executor.Publish(ctx, request, credential, claim)
	require.NoError(t, err)
	require.Equal(t, dispatch.ReplayPublished, result.Disposition)
	require.False(t, result.Claimed)
	result, err = executor.Publish(ctx, request, credential, claim)
	require.NoError(t, err)
	require.Equal(t, dispatch.ReplayPublished, result.Disposition)
	require.EqualValues(t, 1, calls.Load())
	var count int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_replay_controls WHERE resolved_outcome='published' AND active_claim_token IS NULL").Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE action='NOTIFICATION_REPLAY_RESULT'").Scan(&count))
	require.Equal(t, 1, count)
}

func TestReplayPublicationFailureRetainsUncertainty(t *testing.T) {
	for _, kind := range []string{"error", "panic", "timeout", "audit rollback"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f := newReplayRegistrationFixture(t, ctx)
			event := f.acceptedEvent(t, ctx)
			f.quarantineEvent(t, ctx, event)
			request := f.input(t, ctx)
			credential, _ := f.credential(t, ctx, request)
			claim, err := f.service.Claim(ctx, request, credential, request.Positions[0])
			require.NoError(t, err)
			if kind == "audit rollback" {
				_, err = f.pool.Exec(ctx, "ALTER TABLE audit_logs ADD CONSTRAINT reject_result CHECK (action<>'NOTIFICATION_REPLAY_RESULT') NOT VALID")
				require.NoError(t, err)
			}
			var calls atomic.Int32
			executor, err := dispatch.NewReplayExecutor(f.service, scopedPublishFunc(func(publishCtx context.Context, _ kafka.ScopedSendRequested) error {
				calls.Add(1)
				if kind == "timeout" {
					<-publishCtx.Done()
					return publishCtx.Err()
				}
				if kind == "panic" {
					panic("private broker diagnostic")
				}
				if kind == "error" {
					return errors.New("private broker diagnostic")
				}
				return nil
			}))
			require.NoError(t, err)
			result, err := executor.Publish(ctx, request, credential, claim)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private broker diagnostic")
			if kind == "audit rollback" {
				require.Equal(t, dispatch.ReplayAttemptResult{}, result)
			} else {
				require.ErrorIs(t, err, dispatch.ErrReplayPublicationUnconfirmed)
				require.Equal(t, dispatch.ReplayPublishUnconfirmed, result.Disposition)
			}
			_, err = executor.Publish(ctx, request, credential, claim)
			require.ErrorIs(t, err, dispatch.ErrReplayPublicationUnconfirmed)
			require.EqualValues(t, 1, calls.Load(), "even a rolled-back result cannot reuse an in-memory proof for broker I/O")
			_, err = executor.ReplayOne(ctx, request, credential, request.Positions[0])
			require.ErrorIs(t, err, dispatch.ErrReplayPublicationUnconfirmed)
			require.EqualValues(t, 1, calls.Load(), "a new invocation cannot recreate the execution proof")
			var count int
			require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_replay_controls WHERE resolved_outcome IS NOT NULL").Scan(&count))
			require.Zero(t, count)
		})
	}
}

func TestReplayPublicationRevalidatesCurrentState(t *testing.T) {
	for name, change := range map[string]string{
		"revoked grant":        "UPDATE notification_replay_grants SET revoked_at=clock_timestamp()",
		"policy hold":          "UPDATE application_dispatch_policy SET hold_new=true",
		"prepared since claim": `UPDATE notifications SET status='COMPLETED'; INSERT INTO notification_preparations(application_id,notification_id,origin_epoch,source_epoch,resolved_user_count,eligible_user_count,eligible_device_count) SELECT application_id,id,origin_epoch,source_epoch,0,0,0 FROM notifications`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f := newReplayRegistrationFixture(t, ctx)
			event := f.acceptedEvent(t, ctx)
			f.quarantineEvent(t, ctx, event)
			request := f.input(t, ctx)
			credential, _ := f.credential(t, ctx, request)
			claim, err := f.service.Claim(ctx, request, credential, request.Positions[0])
			require.NoError(t, err)
			_, err = f.pool.Exec(ctx, change)
			require.NoError(t, err)
			var calls atomic.Int32
			executor, err := dispatch.NewReplayExecutor(f.service, scopedPublishFunc(func(context.Context, kafka.ScopedSendRequested) error { calls.Add(1); return nil }))
			require.NoError(t, err)
			result, err := executor.Publish(ctx, request, credential, claim)
			if name == "revoked grant" {
				require.ErrorIs(t, err, dispatch.ErrReplayDenied)
			} else {
				require.NoError(t, err)
				want := dispatch.ReplayOnHold
				if name == "prepared since claim" {
					want = dispatch.ReplayPrepared
				}
				require.Equal(t, want, result.Disposition)
			}
			require.Zero(t, calls.Load())
		})
	}
}

func TestReplayLeaseExpiryAndServiceCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	event := f.acceptedEvent(t, ctx)
	f.quarantineEvent(t, ctx, event)
	request := f.input(t, ctx)
	credential, _ := f.credential(t, ctx, request)
	claim, err := f.service.Claim(ctx, request, credential, request.Positions[0])
	require.NoError(t, err)
	var calls atomic.Int32
	executor, err := dispatch.NewReplayExecutor(f.service, scopedPublishFunc(func(context.Context, kafka.ScopedSendRequested) error { calls.Add(1); return nil }))
	require.NoError(t, err)
	// Exercise the production 30-second lease with real database time; do not
	// disable immutable guards or introduce a production clock override for tests.
	select {
	case <-time.After(31 * time.Second):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, err = executor.Publish(ctx, request, credential, claim)
	require.ErrorIs(t, err, dispatch.ErrReplayHeld)
	require.Zero(t, calls.Load())
	_, err = f.pool.Exec(ctx, "UPDATE notification_runtime SET sealed=true; UPDATE notification_log_sources SET active=false; UPDATE staff_users SET is_active=false")
	require.NoError(t, err)
	recovery, err := dispatch.NewReplayRecovery(f.pool)
	require.NoError(t, err)
	report, err := recovery.Tick(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, report.Abandoned)
	_, err = f.pool.Exec(ctx, "UPDATE notification_runtime SET sealed=false; UPDATE notification_log_sources SET active=true; UPDATE staff_users SET is_active=true")
	require.NoError(t, err)
	next := f.input(t, ctx)
	nextCredential, _ := f.credential(t, ctx, next)
	successor, err := f.service.Claim(ctx, next, nextCredential, next.Positions[0])
	require.NoError(t, err)
	require.True(t, successor.Claimed)
	_, err = executor.Publish(ctx, request, credential, claim)
	require.ErrorIs(t, err, dispatch.ErrReplayPublicationUnconfirmed)
	require.Zero(t, calls.Load(), "expired predecessor cannot publish or seize a successor")
	var owner string
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT active_request_id::text FROM notification_replay_controls").Scan(&owner))
	require.Equal(t, next.ID.String(), owner)
}

func TestReplayBrokerAckDatabaseCommitWindows(t *testing.T) {
	for _, committed := range []bool{false, true} {
		name := "commit rolled back"
		if committed {
			name = "commit acknowledgement lost"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f := newReplayRegistrationFixture(t, ctx)
			event := f.acceptedEvent(t, ctx)
			f.quarantineEvent(t, ctx, event)
			request := f.input(t, ctx)
			credential, _ := f.credential(t, ctx, request)
			claim, err := f.service.Claim(ctx, request, credential, request.Positions[0])
			require.NoError(t, err)
			verifier, err := dispatch.NewReplayVerifier("fixture-operator", "fixture-replay", "fixture-deployment", f.key.Public().(ed25519.PublicKey))
			require.NoError(t, err)
			var store dispatch.Transactor = failOutboxCommit{Transactor: f.pool}
			if committed {
				store = replayUncertainCommit{Pool: f.pool}
			}
			requests, err := dispatch.NewReplayRequests(store, verifier, f.source)
			require.NoError(t, err)
			var calls atomic.Int32
			publisher := scopedPublishFunc(func(context.Context, kafka.ScopedSendRequested) error { calls.Add(1); return nil })
			executor, err := dispatch.NewReplayExecutor(requests, publisher)
			require.NoError(t, err)
			result, err := executor.Publish(ctx, request, credential, claim)
			require.Error(t, err)
			require.Equal(t, dispatch.ReplayAttemptResult{}, result)
			normal, err := dispatch.NewReplayExecutor(f.service, publisher)
			require.NoError(t, err)
			result, err = normal.Publish(ctx, request, credential, claim)
			if committed {
				require.NoError(t, err)
				require.Equal(t, dispatch.ReplayPublished, result.Disposition)
			} else {
				require.ErrorIs(t, err, dispatch.ErrReplayPublicationUnconfirmed)
			}
			require.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestReplayRealKafkaAndDuplicatePreparation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	transport := &broker.Transport{DialTimeout: time.Second}
	defer transport.CloseIdleConnections()
	client := &broker.Client{Addr: broker.TCP("127.0.0.1:19092"), Timeout: 3 * time.Second, Transport: transport}
	topic := fmt.Sprintf("replay-fixture-%d", time.Now().UnixNano())
	created, err := client.CreateTopics(ctx, &broker.CreateTopicsRequest{Topics: []broker.TopicConfig{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}}})
	require.NoError(t, err)
	require.NoError(t, created.Errors[topic])
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		_, err := f.pool.Exec(cleanup, "UPDATE notification_runtime SET sealed=true; UPDATE notification_log_sources SET active=false")
		require.NoError(t, err)
		deleted, err := client.DeleteTopics(cleanup, &broker.DeleteTopicsRequest{Topics: []string{topic}})
		require.NoError(t, err)
		require.NoError(t, deleted.Errors[topic])
	}()
	var metadata *broker.MetadataResponse
	require.Eventually(t, func() bool {
		var err error
		metadata, err = client.Metadata(ctx, &broker.MetadataRequest{Topics: []string{topic}})
		return err == nil && len(metadata.Topics) == 1 && metadata.Topics[0].Error == nil && len(metadata.Topics[0].Partitions) == 1 && metadata.Topics[0].Partitions[0].Leader.Host != ""
	}, 5*time.Second, 100*time.Millisecond)
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active) VALUES(gen_random_uuid(),$1,$2,'fixture-generation',true) RETURNING id`, topic, metadata.ClusterID).Scan(&f.source))
	verifier, err := dispatch.NewReplayVerifier("fixture-operator", "fixture-replay", "fixture-deployment", f.key.Public().(ed25519.PublicKey))
	require.NoError(t, err)
	f.service, err = dispatch.NewReplayRequests(f.pool, verifier, f.source)
	require.NoError(t, err)
	event := f.acceptedEvent(t, ctx)
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	quarantine, err := dispatch.NewQuarantine(f.pool)
	require.NoError(t, err)
	require.NoError(t, quarantine.Store(ctx, dispatch.SourceMessage{LogSourceID: f.source, Topic: topic, Offset: 42, Key: []byte("notification_" + strconv.FormatInt(event.NotificationID, 10)), Payload: payload}, dispatch.AdmissionExhausted, 3))
	request := f.input(t, ctx)
	credential, _ := f.credential(t, ctx, request)
	producer := kafka.NewScopedProducer("127.0.0.1:19092", topic)
	defer func() { require.NoError(t, producer.Close()) }()
	executor, err := dispatch.NewReplayExecutor(f.service, producer)
	require.NoError(t, err)
	result, err := executor.ReplayOne(ctx, request, credential, request.Positions[0])
	require.NoError(t, err)
	require.Equal(t, dispatch.ReplayPublished, result.Disposition)
	reader := broker.NewReader(broker.ReaderConfig{Brokers: []string{"127.0.0.1:19092"}, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 100 * time.Millisecond})
	defer func() { require.NoError(t, reader.Close()) }()
	message, err := reader.ReadMessage(ctx)
	require.NoError(t, err)
	decoded, err := kafka.DecodeScopedSend(message.Key, message.Value)
	require.NoError(t, err)
	require.True(t, event.DeadlineAt.Equal(decoded.DeadlineAt))
	require.Equal(t, event.OriginEpoch, decoded.OriginEpoch)
	require.Equal(t, event.SourceEpoch, decoded.SourceEpoch)
	preparer, err := dispatch.NewPreparer(f.pool)
	require.NoError(t, err)
	first, err := preparer.Prepare(ctx, decoded)
	require.NoError(t, err)
	require.True(t, first.SnapshotComplete)
	// The empty committed audience is known; adding a live user must not make a
	// duplicate event establish a new snapshot or claim an unknown denominator.
	_, err = f.pool.Exec(ctx, "INSERT INTO users(application_id,external_id) VALUES($1,'after-replay-snapshot')", f.app)
	require.NoError(t, err)
	require.NoError(t, producer.PublishScopedSend(ctx, decoded))
	duplicate, err := reader.ReadMessage(ctx)
	require.NoError(t, err)
	decoded, err = kafka.DecodeScopedSend(duplicate.Key, duplicate.Value)
	require.NoError(t, err)
	second, err := preparer.Prepare(ctx, decoded)
	require.NoError(t, err)
	require.Equal(t, first, second)
	var count int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_preparations").Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_delivery_intents").Scan(&count))
	require.Zero(t, count)
}

type replayDelayedEvidenceStore struct{ dispatch.Transactor }
type replayDelayedEvidenceTx struct {
	pgx.Tx
	delayed atomic.Bool
}

func (s replayDelayedEvidenceStore) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	tx, err := s.Transactor.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &replayDelayedEvidenceTx{Tx: tx}, nil
}
func (tx *replayDelayedEvidenceTx) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	if strings.Contains(query, "FROM notification_send_dead_letters") && strings.Contains(query, "FOR UPDATE") && !tx.delayed.Swap(true) {
		select {
		case <-time.After(2500 * time.Millisecond):
		case <-ctx.Done():
		}
	}
	return tx.Tx.QueryRow(ctx, query, args...)
}

func TestReplayPublicationRechecksValidityMarginAfterSlowDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f := newReplayRegistrationFixture(t, ctx)
	event := f.acceptedEvent(t, ctx)
	f.quarantineEvent(t, ctx, event)
	request := f.input(t, ctx)
	credential, _ := f.credential(t, ctx, request)
	claim, err := f.service.Claim(ctx, request, credential, request.Positions[0])
	require.NoError(t, err)
	parsed, err := jwt.Parse(credential, func(*jwt.Token) (any, error) { return f.key.Public(), nil })
	require.NoError(t, err)
	claims := parsed.Claims.(jwt.MapClaims)
	claims["exp"] = time.Now().Add(7 * time.Second).Unix()
	short, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(f.key)
	require.NoError(t, err)
	verifier, err := dispatch.NewReplayVerifier("fixture-operator", "fixture-replay", "fixture-deployment", f.key.Public().(ed25519.PublicKey))
	require.NoError(t, err)
	requests, err := dispatch.NewReplayRequests(replayDelayedEvidenceStore{Transactor: f.pool}, verifier, f.source)
	require.NoError(t, err)
	var calls atomic.Int32
	executor, err := dispatch.NewReplayExecutor(requests, scopedPublishFunc(func(context.Context, kafka.ScopedSendRequested) error { calls.Add(1); return nil }))
	require.NoError(t, err)
	_, err = executor.Publish(ctx, request, short, claim)
	require.ErrorIs(t, err, dispatch.ErrReplayHeld)
	require.Zero(t, calls.Load(), "initial validity is insufficient after intervening DB work consumes the margin")
}
