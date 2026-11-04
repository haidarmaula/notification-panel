package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	broker "github.com/segmentio/kafka-go"
)

var ErrScopedSendEnvelope = errors.New("invalid scoped send envelope")

// ScopedSendRequested is the modern acceptance envelope. Legacy envelopes do
// not satisfy this contract; epochs are copied from acceptance, never invented
// by the publisher. Admission must also verify the persisted acceptance.
type ScopedSendRequested struct {
	SchemaVersion  int        `json:"schema_version"`
	ApplicationID  string     `json:"application_id"`
	NotificationID int64      `json:"notification_id"`
	OriginEpoch    string     `json:"origin_epoch"`
	SourceEpoch    string     `json:"source_epoch"`
	RequestedAt    time.Time  `json:"requested_at"`
	ScheduledFor   *time.Time `json:"scheduled_for,omitempty"`
	DeadlineAt     time.Time  `json:"deadline_at"`
}

func (e ScopedSendRequested) Validate() error {
	if e.SchemaVersion != 1 || e.NotificationID <= 0 || e.RequestedAt.IsZero() || e.DeadlineAt.IsZero() ||
		(e.ScheduledFor != nil && e.ScheduledFor.IsZero()) {
		return ErrScopedSendEnvelope
	}
	for _, value := range []string{e.ApplicationID, e.OriginEpoch, e.SourceEpoch} {
		var id pgtype.UUID
		if len(value) != 36 || id.Scan(value) != nil || id.Bytes == [16]byte{} || id.String() != value {
			return ErrScopedSendEnvelope
		}
	}
	return nil
}

// ScopedProducer is reserved for the modern pipeline. It uses true hash routing
// and bounded synchronous writes. Activation requires the documented handoff;
// it does not change the legacy producer's partition mapping.
type ScopedProducer struct {
	writer    *broker.Writer
	transport *broker.Transport
}

func NewScopedProducer(address, topic string) *ScopedProducer {
	transport := &broker.Transport{DialTimeout: time.Second}
	return &ScopedProducer{transport: transport, writer: &broker.Writer{
		Addr: broker.TCP(address), Topic: topic, Balancer: &broker.Hash{},
		RequiredAcks: broker.RequireAll, BatchSize: 1, MaxAttempts: 1,
		ReadTimeout: time.Second, WriteTimeout: time.Second, Transport: transport,
	}}
}

// PublishScopedSend receives immutable buffers. Cancellation can leave an
// uncertain broker write; the durable outbox and admission dedup absorb it.
func (p *ScopedProducer) PublishScopedSend(ctx context.Context, event ScopedSendRequested) error {
	if err := event.Validate(); err != nil {
		return err
	}
	value, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, broker.Message{
		Key: []byte("notification_" + strconv.FormatInt(event.NotificationID, 10)), Value: value,
	})
}

// Close is called after all publish calls have stopped, including cancellation.
func (p *ScopedProducer) Close() error {
	err := p.writer.Close()
	p.transport.CloseIdleConnections()
	return err
}
