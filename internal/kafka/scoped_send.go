package kafka

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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

// DecodeScopedSend rejects legacy/malformed envelopes and ambiguous JSON. A
// version-1 schema change requires coordinated versioning, not silent defaults.
// Errors never contain raw keys, payloads or decoder values.
func DecodeScopedSend(key, value []byte) (ScopedSendRequested, error) {
	invalid := func() (ScopedSendRequested, error) { return ScopedSendRequested{}, ErrScopedSendEnvelope }
	if len(value) == 0 || len(value) > 64*1024 || len(key) > 64 {
		return invalid()
	}
	d := json.NewDecoder(bytes.NewReader(value))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return invalid()
	}
	seen := make(map[string]bool)
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return invalid()
		}
		// encoding/json otherwise accepts case aliases for tagged struct fields.
		switch name {
		case "schema_version", "application_id", "notification_id", "origin_epoch", "source_epoch", "requested_at", "scheduled_for", "deadline_at":
			seen[name] = true
		default:
			return invalid()
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return invalid()
		}
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return invalid()
	}
	var extra json.RawMessage
	if err := d.Decode(&extra); err != io.EOF {
		return invalid()
	}
	d = json.NewDecoder(bytes.NewReader(value))
	d.DisallowUnknownFields()
	var event ScopedSendRequested
	if err := d.Decode(&event); err != nil || event.Validate() != nil ||
		string(key) != "notification_"+strconv.FormatInt(event.NotificationID, 10) {
		return invalid()
	}
	return event, nil
}

// ScopedProducer is reserved for the modern pipeline. It uses true hash routing
// and synchronous acknowledgements with per-attempt transport bounds. A canceled
// call can retain producer-owned I/O; Close drains it. Activation requires handoff.
type ScopedProducer struct {
	writer    *broker.Writer
	transport *broker.Transport
}

// NewScopedProducer constructs a plaintext producer for isolated fixtures.
// Deployed scoped runtimes must use NewAuthenticatedScopedProducer.
func NewScopedProducer(address, topic string) *ScopedProducer {
	transport := &broker.Transport{DialTimeout: time.Second}
	return newScopedProducer([]string{address}, topic, transport)
}

func newScopedProducer(addresses []string, topic string, transport *broker.Transport) *ScopedProducer {
	return &ScopedProducer{transport: transport, writer: &broker.Writer{
		Addr: broker.TCP(addresses...), Topic: topic, Balancer: &broker.Hash{},
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
