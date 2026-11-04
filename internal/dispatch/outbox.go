// Package dispatch implements durable send publication, audience preparation,
// source-aware quarantine and serial Kafka admission.
package dispatch

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
)

// Transactor supplies transactions owned and completed by dispatch operations.
type Transactor interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// Publisher synchronously acknowledges broker acceptance of a scoped send.
// An error may still leave the message accepted or producer-owned I/O active.
// Callers must tolerate replay and own shutdown/drain before source transitions.
type Publisher interface {
	PublishScopedSend(context.Context, kafka.ScopedSendRequested) error
}

// Outcome distinguishes idle or held polls from published or deferred outbox work.
type Outcome string

const (
	Idle      Outcome = "idle"
	Held      Outcome = "held"
	Published Outcome = "published"
	Deferred  Outcome = "deferred"
)

// Outbox owns no goroutines. The scheduler must bound each tick and back off on
// database errors. One invocation locks at most one outbox through broker I/O.
type Outbox struct {
	db        Transactor
	publisher Publisher
}

// NewOutbox requires a database and synchronous publisher; it starts no workers.
func NewOutbox(db Transactor, publisher Publisher) (*Outbox, error) {
	if db == nil || publisher == nil {
		return nil, errors.New("outbox requires database and publisher")
	}
	return &Outbox{db: db, publisher: publisher}, nil
}

func publishBackoff(attempts int32) time.Duration {
	// Cap before shifting; corrupted/very old attempt counts cannot overflow.
	n := min(max(attempts, 0), 8)
	base := time.Second * time.Duration(1<<n)
	return min(5*time.Minute, base+time.Duration(rand.Int64N(int64(base/4)+1)))
}

// DrainOne never retries its transaction: broker acknowledgement is external
// and can survive a rollback. A failed/uncertain commit returns an error and
// leaves reconciliation to the next poll and durable consumer deduplication.
func (o *Outbox) DrainOne(ctx context.Context) (Outcome, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := o.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New(tx)
	runtime, err := q.LockNotificationRuntime(ctx)
	if err != nil {
		return "", err
	}
	if runtime.Sealed {
		return Held, nil
	}
	row, err := q.LockNextScopedSendOutbox(ctx, sqlc.LockNextScopedSendOutboxParams{OriginEpoch: runtime.OriginEpoch, SourceEpoch: runtime.SourceEpoch})
	if errors.Is(err, pgx.ErrNoRows) {
		return Idle, nil
	}
	if err != nil {
		return "", err
	}
	event := kafka.ScopedSendRequested{SchemaVersion: 1, ApplicationID: row.ApplicationPublicID.String(),
		NotificationID: row.NotificationID, OriginEpoch: row.OriginEpoch.String(), SourceEpoch: row.SourceEpoch.String(),
		RequestedAt: row.RequestedAt.Time, DeadlineAt: row.DeadlineAt.Time}
	if row.ScheduledFor.Valid {
		at := row.ScheduledFor.Time
		event.ScheduledFor = &at
	}
	if err := event.Validate(); err != nil {
		return "", err
	}
	brokerCtx, stopPublish := context.WithTimeout(ctx, 2*time.Second)
	err = o.publisher.PublishScopedSend(brokerCtx, event)
	stopPublish()
	outcome := Published
	if err != nil {
		outcome = Deferred
		err = q.DeferSendOutbox(ctx, sqlc.DeferSendOutboxParams{ID: row.ID, DelaySeconds: publishBackoff(row.PublishAttempts).Seconds()})
	} else {
		var n int64
		n, err = q.DeletePublishedSendOutbox(ctx, row.ID)
		if err == nil && n != 1 {
			err = errors.New("locked outbox disappeared")
		}
	}
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return outcome, nil
}
