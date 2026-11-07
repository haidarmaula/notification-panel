//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	broker "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/kafka"
)

var errProjectionHeld = errors.New("delivery projection held")

// This test-only consumer uses committed PostgreSQL transactions. Its source
// identity comes from its reader configuration and its approved delivery scope
// comes from a trusted snapshot. Neither is learned from an incoming message.
type contractConsumer struct {
	pool      *pgxpool.Pool
	logSource string
}

func (c contractConsumer) apply(ctx context.Context, msg broker.Message) (string, error) {
	e, err := kafka.DecodeScopedDelivery(msg.Value)
	if err != nil || !bytes.Equal(msg.Key, kafka.DeliveryUpdateKey(e.NotificationID)) {
		return "", errProjectionHeld
	}
	var outcome string
	digest := sha256.Sum256(msg.Value)
	err = pgx.BeginFunc(ctx, c.pool, func(tx pgx.Tx) error {
		var highest int64
		err := tx.QueryRow(ctx, `SELECT p.version FROM consumer_scope s JOIN consumer_projection p
		 ON (p.source,p.delivery)=(s.source,s.delivery) WHERE s.delivery=$1 AND s.app=$2 AND s.origin=$3
		 AND s.source=$4 AND s.campaign=$5 AND s.recipient=$6 AND s.log_source=$7 AND s.topic=$8
		 FOR UPDATE OF s,p`, e.DeliveryID, e.ApplicationID, e.OriginEpoch, e.SourceEpoch, e.NotificationID, e.UserID, c.logSource, msg.Topic).Scan(&highest)
		if errors.Is(err, pgx.ErrNoRows) {
			return errProjectionHeld
		}
		if err != nil {
			return err
		}
		claim, err := tx.Exec(ctx, `INSERT INTO consumer_inbox(event,source,delivery,version,digest) VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT DO NOTHING`, e.EventID, e.SourceEpoch, e.DeliveryID, e.DeliveryVersion, digest[:])
		if err != nil {
			return err
		}
		if claim.RowsAffected() == 0 {
			var same bool
			err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM consumer_inbox WHERE event=$1 AND digest=$2)`, e.EventID, digest[:]).Scan(&same)
			if err != nil {
				return err
			}
			if !same {
				return errProjectionHeld
			}
			outcome = "duplicate"
			return nil
		}
		// An approved rebuild baseline can predate its inbox history.
		if e.DeliveryVersion == highest {
			return errProjectionHeld
		}
		if e.DeliveryVersion < highest {
			outcome = "stale"
			return nil
		}
		// The gap policy applies the complete newer business projection. Store
		// facts instead of adding a counter for each received Kafka message.
		_, err = tx.Exec(ctx, `UPDATE consumer_projection SET version=$3, facts=$4::jsonb - ARRAY[
		 'event_id','schema_version','event_kind','occurred_at','source_epoch','delivery_version']::text[]
		 WHERE source=$1 AND delivery=$2`, e.SourceEpoch, e.DeliveryID, e.DeliveryVersion, string(msg.Value))
		outcome = "applied"
		return err
	})
	if err != nil {
		return "", err
	}
	return outcome, nil
}

func TestScopedDeliveryConsumerContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	f, source := newUpdateFixture(t, ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := f.pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	exec(`CREATE TABLE consumer_scope(delivery uuid PRIMARY KEY, app uuid NOT NULL, origin uuid NOT NULL,
	 source uuid NOT NULL, campaign bigint NOT NULL, recipient bigint NOT NULL, log_source uuid NOT NULL, topic text NOT NULL);
	 CREATE TABLE consumer_projection(source uuid NOT NULL, delivery uuid NOT NULL, version bigint NOT NULL,
	 facts jsonb NOT NULL, PRIMARY KEY(source,delivery));
	 CREATE TABLE consumer_inbox(event uuid PRIMARY KEY, source uuid NOT NULL, delivery uuid NOT NULL,
	 version bigint NOT NULL, digest bytea NOT NULL, UNIQUE(source,delivery,version))`)
	exec(`INSERT INTO consumer_scope SELECT d.id,a.public_id,n.origin_epoch,n.source_epoch,n.id,d.owner_user_id,$2,$3
	 FROM notification_delivery_intents d JOIN notifications n ON (n.application_id,n.id)=(d.application_id,d.notification_id)
	 JOIN applications a ON a.id=n.application_id WHERE d.id=$1`, f.delivery, source.ID, source.Topic)
	exec(`INSERT INTO consumer_projection SELECT source,delivery,0,'{}'::jsonb FROM consumer_scope`)

	// Consume a real outbox record from the isolated broker. The test controls
	// the topic incarnation and all fixture records after this starting offset.
	offset := lastTopicOffset(t, ctx, source.Topic)
	producer := kafka.NewScopedProducer("127.0.0.1:19092", source.Topic)
	defer func() { require.NoError(t, producer.Close()) }()
	written, err := updateService(t, f, source, producer).DrainOne(ctx)
	require.NoError(t, err)
	require.Equal(t, dispatch.Published, written)
	readFirst := func() broker.Message {
		t.Helper()
		reader := broker.NewReader(broker.ReaderConfig{Brokers: []string{"127.0.0.1:19092"}, Topic: source.Topic, Partition: 0, MinBytes: 1, MaxBytes: 65536, MaxWait: 100 * time.Millisecond})
		defer func() { require.NoError(t, reader.Close()) }()
		require.NoError(t, reader.SetOffset(offset))
		msg, err := reader.ReadMessage(ctx)
		require.NoError(t, err)
		return msg
	}
	first := readFirst()
	v1, err := kafka.DecodeScopedDelivery(first.Value)
	require.NoError(t, err)
	require.Equal(t, f.delivery.String(), v1.DeliveryID)
	newID := func() string { return replayUUID(t, ctx, f.pool).String() }
	message := func(e kafka.ScopedDeliveryUpdated) broker.Message {
		t.Helper()
		raw, err := json.Marshal(e)
		require.NoError(t, err)
		return broker.Message{Topic: source.Topic, Key: kafka.DeliveryUpdateKey(e.NotificationID), Value: raw}
	}
	reopen := func() contractConsumer {
		t.Helper()
		pool, err := pgxpool.NewWithConfig(ctx, f.pool.Config())
		require.NoError(t, err)
		return contractConsumer{pool: pool, logSource: source.ID.String()}
	}
	c := reopen()
	defer func() { c.pool.Close() }()
	want := func(m broker.Message, expected string) {
		t.Helper()
		got, err := c.apply(ctx, m)
		require.NoError(t, err)
		require.Equal(t, expected, got)
	}
	held := func(m broker.Message) {
		t.Helper()
		_, err := c.apply(ctx, m)
		require.ErrorIs(t, err, errProjectionHeld)
	}
	assertState := func(version int64, status string, seen int) {
		t.Helper()
		var gotVersion int64
		var gotStatus string
		var rows int
		require.NoError(t, f.pool.QueryRow(ctx, `SELECT p.version,p.facts->>'status',(SELECT count(*) FROM consumer_inbox)
		 FROM consumer_scope s JOIN consumer_projection p ON (p.source,p.delivery)=(s.source,s.delivery)`).Scan(&gotVersion, &gotStatus, &rows))
		require.Equal(t, version, gotVersion)
		require.Equal(t, status, gotStatus)
		require.Equal(t, seen, rows)
	}
	want(first, "applied")
	assertState(1, "SENT", 1)
	c.pool.Close()
	c = reopen() // A fresh reader restarts at the same offset after the DB commit.
	want(readFirst(), "duplicate")
	v2 := v1
	v2.EventKind = "observation"
	v2.EventID = newID()
	v2.DeliveryVersion = 2
	v2.Status = "DELIVERED"
	v2.ObservedDelivered = true
	v3 := v2
	v3.EventID = newID()
	v3.DeliveryVersion = 3
	v3.Status = "OPENED"
	v3.ObservedOpened = true
	exec(`ALTER TABLE consumer_projection ADD CONSTRAINT fixture_write_failure CHECK (version < 3)`)
	_, err = c.apply(ctx, message(v3))
	require.ErrorContains(t, err, "fixture_write_failure")
	assertState(1, "SENT", 1) // A failed projection write also rolled back its dedup claim.
	exec(`ALTER TABLE consumer_projection DROP CONSTRAINT fixture_write_failure`)
	want(message(v3), "applied")
	want(message(v2), "stale")
	want(first, "duplicate")
	want(message(v2), "duplicate")
	assertState(3, "OPENED", 3)
	var completeFacts bool
	require.NotNil(t, v1.ProviderMessageID)
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT (facts->>'observed_opened')::boolean
		AND (facts->>'observed_delivered')::boolean AND facts->>'provider_message_id'=$1
		AND facts->>'failed_at' IS NULL FROM consumer_projection`, *v1.ProviderMessageID).Scan(&completeFacts))
	require.True(t, completeFacts)
	conflict := v2
	conflict.EventID = newID()
	held(message(conflict)) // An older version also retains its claim.
	conflict = v3
	conflict.Status = "DELIVERED"
	conflict.ObservedOpened = false
	held(message(conflict)) // Reused event ID, altered facts.
	conflict.EventID = newID()
	held(message(conflict)) // Reused version, different event ID.

	newSource := newID()
	foreign := v1
	foreign.EventID = newID()
	foreign.SourceEpoch = newSource
	held(message(foreign))
	// Use an unclaimed higher version, so the inbox cannot mask a scope error.
	outside := v1
	outside.DeliveryVersion = 4
	wrong := outside
	wrong.EventID = newID()
	wrong.ApplicationID = newID()
	held(message(wrong))
	wrong = outside
	wrong.EventID = newID()
	wrong.OriginEpoch = newID()
	held(message(wrong))
	wrong = outside
	wrong.EventID = newID()
	wrong.UserID++
	held(message(wrong))
	wrong = outside
	wrong.EventID = newID()
	wrong.NotificationID++
	held(message(wrong))
	bad := first
	bad.Key = []byte("delivery_0")
	held(bad)
	bad = first
	bad.Topic = "replacement.topic"
	held(bad)
	untrusted := c
	untrusted.logSource = newID()
	_, err = untrusted.apply(ctx, first)
	require.ErrorIs(t, err, errProjectionHeld)
	assertState(3, "OPENED", 3)

	// An independently approved manifest reconciles the restored source with
	// the external OPENED projection. Preserve the old lineage and its inbox;
	// seed a reviewed floor and activate the replacement in one transaction.
	err = pgx.BeginFunc(ctx, f.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO consumer_projection SELECT $1,delivery,2,facts FROM consumer_projection WHERE source=$2`, newSource, v1.SourceEpoch)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE consumer_scope SET source=$1 WHERE source=$2`, newSource, v1.SourceEpoch)
		return err
	})
	require.NoError(t, err)
	want(message(foreign), "stale")
	held(first)
	relabel := v1
	relabel.SourceEpoch = newSource
	relabel.DeliveryVersion = 4 // Unclaimed slot; only the global event ID exposes the relabel.
	held(message(relabel))
	assertState(2, "OPENED", 4)
	next := v3
	next.EventID = newID()
	next.SourceEpoch = newSource
	want(message(next), "applied")
	assertState(3, "OPENED", 5)
	var oldVersion int64
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT version FROM consumer_projection WHERE source=$1 AND delivery=$2`, v1.SourceEpoch, v1.DeliveryID).Scan(&oldVersion))
	require.EqualValues(t, 3, oldVersion)
}
