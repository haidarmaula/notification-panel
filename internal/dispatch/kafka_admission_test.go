package dispatch

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
)

type approvalQueryFixture struct {
	sqlc.DBTX
	check func(context.Context)
	err   error
}

func (f approvalQueryFixture) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	f.check(ctx)
	return approvalErrorRow{err: f.err}
}

type approvalErrorRow struct{ err error }

func (r approvalErrorRow) Scan(...any) error { return r.err }

func TestKafkaSourceApprovalBoundsDatabaseWait(t *testing.T) {
	for _, name := range []string{"default deadline", "earlier caller deadline"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			var callerDeadline time.Time
			if name == "earlier caller deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
				callerDeadline, _ = ctx.Deadline()
			}
			stopped := errors.New("fixture database failure")
			calls := 0
			queries := sqlc.New(approvalQueryFixture{err: stopped, check: func(ctx context.Context) {
				calls++
				deadline, ok := ctx.Deadline()
				require.True(t, ok, "source approval must not wait for the whole generation lifetime")
				require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
				if !callerDeadline.IsZero() {
					require.Equal(t, callerDeadline, deadline)
				}
			}})
			reader := generationAdmissionReader{q: queries}
			require.ErrorIs(t, reader.approved(ctx), stopped)
			require.Equal(t, 1, calls)
		})
	}
}

func authenticatedAdmissionFixture(t *testing.T) KafkaAdmissionConfig {
	t.Helper()
	var source pgtype.UUID
	require.NoError(t, source.Scan("00000000-0000-4000-8000-000000000001"))
	return KafkaAdmissionConfig{Brokers: []string{"broker.example.test:9093"}, GroupID: "admission-fixture", SourceID: source, ClusterID: "cluster", Topic: "send", Incarnation: "reviewed", PartitionCount: 2, TLS: &tls.Config{MinVersion: tls.VersionTLS12}, SASL: plain.Mechanism{Username: "fixture", Password: "fixture_only"}}
}

func TestAuthenticatedKafkaAdmissionRejectsUnsafePolicy(t *testing.T) {
	preparer := prepareFixture(func(context.Context, kafka.ScopedSendRequested) (PreparationResult, error) {
		t.Fatal("constructor performed preparation")
		return PreparationResult{}, nil
	})
	quarantine := quarantineFixture(func(context.Context, SourceMessage, QuarantineReason, int32) error {
		t.Fatal("constructor performed quarantine")
		return nil
	})
	for _, tt := range []struct {
		name   string
		change func(*KafkaAdmissionConfig)
	}{
		{"plaintext", func(c *KafkaAdmissionConfig) { c.TLS = nil; c.SASL = nil }},
		{"unverified TLS", func(c *KafkaAdmissionConfig) { c.TLS.InsecureSkipVerify = true }},
		{"old TLS", func(c *KafkaAdmissionConfig) { c.TLS.MinVersion = tls.VersionTLS10 }},
		{"anonymous TLS", func(c *KafkaAdmissionConfig) { c.SASL = nil }},
		{"unknown partitions", func(c *KafkaAdmissionConfig) { c.PartitionCount = 0 }},
		{"invalid group", func(c *KafkaAdmissionConfig) { c.GroupID = "group\nforged" }},
		{"empty broker", func(c *KafkaAdmissionConfig) { c.Brokers = []string{""} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := authenticatedAdmissionFixture(t)
			tt.change(&cfg)
			consumer, err := NewAuthenticatedKafkaAdmission(sqlc.New(nil), cfg, preparer, quarantine)
			require.Error(t, err)
			require.Nil(t, consumer)
		})
	}
}

func TestAuthenticatedKafkaAdmissionCopiesTransportPolicy(t *testing.T) {
	cfg := authenticatedAdmissionFixture(t)
	preparer := prepareFixture(func(context.Context, kafka.ScopedSendRequested) (PreparationResult, error) {
		return PreparationResult{}, nil
	})
	quarantine := quarantineFixture(func(context.Context, SourceMessage, QuarantineReason, int32) error { return nil })
	consumer, err := NewAuthenticatedKafkaAdmission(sqlc.New(nil), cfg, preparer, quarantine)
	require.NoError(t, err)
	cfg.Brokers[0] = "changed:1"
	cfg.TLS.InsecureSkipVerify = true
	require.Equal(t, "broker.example.test:9093", consumer.config.Brokers[0])
	require.False(t, consumer.config.TLS.InsecureSkipVerify)
	dialer := consumer.config.dialer()
	require.Same(t, consumer.config.TLS, dialer.TLS)
	require.Equal(t, "PLAIN", dialer.SASLMechanism.Name())
	require.Equal(t, time.Second, dialer.Timeout)
}
