package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
)

// UpdatePublisher synchronously acknowledges exact persisted bytes. It must bind
// the approved destination, verify broker metadata, and own Close/drain on shutdown.
// An error can still leave the message accepted or producer-owned I/O active.
type UpdatePublisher interface {
	PublishScopedDelivery(context.Context, []byte) error
}

// UpdateOutbox owns no goroutines and drains one predecessor-eligible row per
// call. The polling owner bounds calls/backoff and closes its publisher separately.
type UpdateOutbox struct {
	db        Transactor
	publisher UpdatePublisher
	source    SourceManifest
}

// NewUpdateOutbox requires an explicit reviewed source; construction performs no
// I/O. Database approval and live broker metadata remain per-publication checks.
func NewUpdateOutbox(db Transactor, publisher UpdatePublisher, source SourceManifest) (*UpdateOutbox, error) {
	if db == nil || publisher == nil || source.Validate() != nil {
		return nil, ErrQuarantineSource
	}
	return &UpdateOutbox{db: db, publisher: publisher, source: source}, nil
}

// DrainOne never retries a transaction that performed broker I/O. Ack/commit
// uncertainty retains at-least-once semantics and requires consumer ID/version dedup.
func (o *UpdateOutbox) DrainOne(parent context.Context) (Outcome, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	tx, err := o.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return "", err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
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
	approved, err := o.source.approved(ctx, q)
	if err != nil {
		return "", err
	}
	if !approved {
		return Held, nil
	}
	row, err := q.LockNextDeliveryUpdate(ctx, sqlc.LockNextDeliveryUpdateParams{OriginEpoch: runtime.OriginEpoch, SourceEpoch: runtime.SourceEpoch})
	if errors.Is(err, pgx.ErrNoRows) {
		blocked, err := q.HasLineageBlockedDeliveryUpdate(ctx, sqlc.HasLineageBlockedDeliveryUpdateParams{OriginEpoch: runtime.OriginEpoch, SourceEpoch: runtime.SourceEpoch})
		if err != nil {
			return "", err
		}
		if blocked {
			return Held, nil
		}
		return Idle, nil
	}
	if err != nil {
		return "", err
	}
	event, decodeErr := kafka.DecodeScopedDelivery(row.Payload)
	valid := decodeErr == nil && event.EventID == row.EventID.String() && event.ApplicationID == row.ApplicationPublicID.String() &&
		event.NotificationID == row.NotificationID && event.UserID == row.OwnerUserID && event.DeliveryID == row.DeliveryID.String() && event.DeliveryVersion == row.DeliveryVersion &&
		event.OriginEpoch == row.OriginEpoch.String() && event.SourceEpoch == row.SourceEpoch.String() && event.OccurredAt.Equal(row.OccurredAt.Time)
	outcome := Held
	if valid {
		brokerCtx, stop := context.WithTimeout(ctx, 2*time.Second)
		publishErr := o.publisher.PublishScopedDelivery(brokerCtx, row.Payload)
		stop()
		if publishErr == nil {
			n, err := q.DeletePublishedDeliveryUpdate(ctx, row.EventID)
			if err != nil {
				return "", err
			}
			if n != 1 {
				return "", ErrExecutionUnavailable
			}
			outcome = Published
		} else {
			outcome = Deferred
		}
	}
	if outcome != Published {
		n, err := q.DeferDeliveryUpdate(ctx, sqlc.DeferDeliveryUpdateParams{EventID: row.EventID, DelaySeconds: publishBackoff(row.Attempts).Seconds()})
		if err != nil {
			return "", err
		}
		if n != 1 {
			return "", ErrExecutionUnavailable
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return outcome, nil
}
