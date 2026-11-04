package dispatch

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

var (
	ErrQuarantineSource = errors.New("send log source is not approved or evidence conflicts")
	ErrQuarantineInput  = errors.New("invalid send quarantine evidence")
)

// QuarantineReason is an allowlisted admission failure class, never raw error text.
type QuarantineReason string

const (
	InvalidEnvelope    QuarantineReason = "invalid_envelope"
	AcceptanceMismatch QuarantineReason = "acceptance_mismatch"
	UnsupportedTargets QuarantineReason = "unsupported_targets"
	PreparationHeld    QuarantineReason = "preparation_held"
	PreparationState   QuarantineReason = "preparation_state"
	AdmissionExhausted QuarantineReason = "admission_exhausted"
)

// SourceMessage identifies the physical log position. Topic name alone is not a
// log incarnation. Key and payload are sensitive; never log this struct.
type SourceMessage struct {
	LogSourceID pgtype.UUID
	Topic       string
	Partition   int32
	Offset      int64
	Key         []byte
	Payload     []byte
}

// Quarantine persists source-position evidence independently of campaign outcomes.
type Quarantine struct{ db Transactor }

// NewQuarantine requires a database; source registration and approval are external.
func NewQuarantine(db Transactor) (*Quarantine, error) {
	if db == nil {
		return nil, errors.New("quarantine requires database")
	}
	return &Quarantine{db: db}, nil
}

// Store commits sensitive raw evidence before the caller may acknowledge it.
// Failure never makes a source offset safe. Exact redelivery is idempotent;
// conflicting bytes at one position fail closed rather than overwrite evidence.
func (s *Quarantine) Store(ctx context.Context, message SourceMessage, reason QuarantineReason, attempts int32) error {
	if !message.LogSourceID.Valid || message.LogSourceID.Bytes == [16]byte{} || message.Partition < 0 || message.Offset < 0 ||
		len(message.Key) > 1<<20 || len(message.Payload) > 1<<20 || attempts < 1 || attempts > 3 {
		return ErrQuarantineInput
	}
	switch reason {
	case InvalidEnvelope, AcceptanceMismatch, UnsupportedTargets, PreparationHeld, PreparationState, AdmissionExhausted:
	default:
		return ErrQuarantineInput
	}
	// Keep non-null empty bytea for unkeyed/empty malformed Kafka messages.
	key, payload := make([]byte, len(message.Key)), make([]byte, len(message.Payload))
	copy(key, message.Key)
	copy(payload, message.Payload)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New(tx)
	runtime, err := q.LockNotificationRuntime(ctx)
	if err != nil {
		return err
	}
	if runtime.Sealed {
		return ErrQuarantineSource
	}
	source, err := q.LockSendLogSource(ctx, message.LogSourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrQuarantineSource
	}
	if err != nil {
		return err
	}
	if !source.Active || source.Topic != message.Topic {
		return ErrQuarantineSource
	}
	n, err := q.InsertSendQuarantine(ctx, sqlc.InsertSendQuarantineParams{LogSourceID: message.LogSourceID,
		OriginalPartition: message.Partition, OriginalOffset: message.Offset, OriginalKey: key, OriginalPayload: payload,
		Reason: string(reason), Attempts: attempts})
	if err != nil {
		return err
	}
	if n == 0 {
		stored, err := q.GetSendQuarantine(ctx, sqlc.GetSendQuarantineParams{LogSourceID: message.LogSourceID, OriginalPartition: message.Partition, OriginalOffset: message.Offset})
		if err != nil {
			return err
		}
		if !bytes.Equal(stored.OriginalKey, key) || !bytes.Equal(stored.OriginalPayload, payload) {
			return ErrQuarantineSource
		}
	}
	return tx.Commit(ctx)
}
