package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/kafka"
)

type admissionReaderFixture struct {
	verify func(context.Context) error
	fetch  func(context.Context) (AdmissionMessage, error)
}

func (f admissionReaderFixture) VerifySource(ctx context.Context) error { return f.verify(ctx) }
func (f admissionReaderFixture) Fetch(ctx context.Context) (AdmissionMessage, error) {
	return f.fetch(ctx)
}

type prepareFixture func(context.Context, kafka.ScopedSendRequested) (PreparationResult, error)

func (f prepareFixture) Prepare(ctx context.Context, event kafka.ScopedSendRequested) (PreparationResult, error) {
	return f(ctx, event)
}

type quarantineFixture func(context.Context, SourceMessage, QuarantineReason, int32) error

func (f quarantineFixture) Store(ctx context.Context, m SourceMessage, r QuarantineReason, a int32) error {
	return f(ctx, m, r, a)
}

func admissionFixtureMessage(t *testing.T, id int64) AdmissionMessage {
	t.Helper()
	event := kafka.ScopedSendRequested{SchemaVersion: 1, NotificationID: id,
		ApplicationID: "11111111-1111-4111-8111-111111111111", OriginEpoch: "22222222-2222-4222-8222-222222222222",
		SourceEpoch: "33333333-3333-4333-8333-333333333333", RequestedAt: time.Now().UTC(), DeadlineAt: time.Now().UTC().Add(time.Hour)}
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	return AdmissionMessage{Source: SourceMessage{Key: []byte("notification_" + strconv.FormatInt(id, 10)), Payload: payload}}
}

func TestSerialAdmissionOrder(t *testing.T) {
	var order []string
	index := 0
	reader := admissionReaderFixture{
		verify: func(context.Context) error { order = append(order, "verify"); return nil },
		fetch: func(context.Context) (AdmissionMessage, error) {
			order = append(order, "fetch")
			if index == 2 {
				return AdmissionMessage{}, io.EOF
			}
			index++
			message := admissionFixtureMessage(t, int64(index))
			message.Acknowledge = func(context.Context) error { order = append(order, "ack"); return nil }
			return message, nil
		},
	}
	preparer := prepareFixture(func(context.Context, kafka.ScopedSendRequested) (PreparationResult, error) {
		order = append(order, "prepare")
		return PreparationResult{SnapshotComplete: true}, nil
	})
	quarantine := quarantineFixture(func(context.Context, SourceMessage, QuarantineReason, int32) error {
		t.Fatal("valid prepared work must not be quarantined")
		return nil
	})
	consumer, err := NewSerialConsumer(reader, preparer, quarantine)
	require.NoError(t, err)
	require.ErrorIs(t, consumer.Run(context.Background()), io.EOF)
	require.Equal(t, []string{"verify", "fetch", "prepare", "ack", "fetch", "prepare", "ack", "fetch"}, order)
}

func TestSerialAdmissionUnsafeEvidence(t *testing.T) {
	for _, cancelStorage := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelStorage), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fetches, stores, acks := 0, 0, 0
			reader := admissionReaderFixture{verify: func(context.Context) error { return nil }, fetch: func(context.Context) (AdmissionMessage, error) {
				fetches++
				if fetches > 1 {
					return AdmissionMessage{}, io.EOF
				}
				return AdmissionMessage{Source: SourceMessage{Key: []byte{0xff}, Payload: []byte("bad")}, Acknowledge: func(context.Context) error { acks++; return nil }}, nil
			}}
			preparer := prepareFixture(func(context.Context, kafka.ScopedSendRequested) (PreparationResult, error) {
				t.Fatal("malformed message must not reach preparation")
				return PreparationResult{}, nil
			})
			quarantine := quarantineFixture(func(_ context.Context, message SourceMessage, reason QuarantineReason, attempts int32) error {
				stores++
				require.Equal(t, 1, fetches)
				require.Zero(t, acks)
				require.Equal(t, InvalidEnvelope, reason)
				require.EqualValues(t, 1, attempts)
				require.Equal(t, []byte{0xff}, message.Key)
				if cancelStorage {
					cancel()
				}
				if stores < 3 {
					return errors.New("database unavailable")
				}
				return nil
			})
			consumer, err := NewSerialConsumer(reader, preparer, quarantine)
			require.NoError(t, err)
			consumer.wait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
			err = consumer.Run(ctx)
			if cancelStorage {
				require.ErrorIs(t, err, context.Canceled)
				require.Zero(t, acks)
				require.Equal(t, 1, fetches)
			} else {
				require.ErrorIs(t, err, io.EOF)
				require.Equal(t, 1, acks)
				require.Equal(t, 3, stores)
			}
		})
	}
}

func TestSerialAdmissionRetryAndAckFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		panic    bool
		reason   QuarantineReason
		attempts int32
	}{
		{"database", errors.New("temporary database failure"), false, AdmissionExhausted, 3},
		{"hold", ErrAdmissionHeld, false, PreparationHeld, 3},
		{"envelope", ErrAdmissionEnvelope, false, AcceptanceMismatch, 1},
		{"targets", ErrAdmissionTarget, false, UnsupportedTargets, 1},
		{"state", ErrAdmissionState, false, PreparationState, 1},
		{"panic", nil, true, AdmissionExhausted, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			var prepares, stores, fetches int32
			message := admissionFixtureMessage(t, 42)
			message.Acknowledge = func(context.Context) error { return ErrAssignmentLost }
			reader := admissionReaderFixture{verify: func(context.Context) error { return nil }, fetch: func(context.Context) (AdmissionMessage, error) { fetches++; return message, nil }}
			preparer := prepareFixture(func(context.Context, kafka.ScopedSendRequested) (PreparationResult, error) {
				prepares++
				if test.panic {
					panic("sensitive panic detail")
				}
				return PreparationResult{}, test.err
			})
			quarantine := quarantineFixture(func(_ context.Context, _ SourceMessage, reason QuarantineReason, attempts int32) error {
				stores++
				require.Equal(t, test.reason, reason)
				require.Equal(t, test.attempts, attempts)
				return nil
			})
			consumer, err := NewSerialConsumer(reader, preparer, quarantine)
			require.NoError(t, err)
			consumer.wait = func(context.Context, time.Duration) error { return nil }
			require.ErrorIs(t, consumer.Run(context.Background()), ErrAssignmentLost)
			require.Equal(t, test.attempts, prepares)
			require.EqualValues(t, 1, stores)
			require.EqualValues(t, 1, fetches, "ack failure must stop before another fetch")
		})
	}
}

func TestSerialAdmissionSourceAndShutdown(t *testing.T) {
	for _, verified := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		fetches, stores, acks := 0, 0, 0
		reader := admissionReaderFixture{verify: func(context.Context) error {
			if !verified {
				return ErrQuarantineSource
			}
			return nil
		}, fetch: func(context.Context) (AdmissionMessage, error) {
			fetches++
			message := admissionFixtureMessage(t, 42)
			message.Acknowledge = func(context.Context) error { acks++; return nil }
			return message, nil
		}}
		preparer := prepareFixture(func(context.Context, kafka.ScopedSendRequested) (PreparationResult, error) {
			cancel()
			return PreparationResult{}, context.Canceled
		})
		quarantine := quarantineFixture(func(context.Context, SourceMessage, QuarantineReason, int32) error { stores++; return nil })
		consumer, err := NewSerialConsumer(reader, preparer, quarantine)
		require.NoError(t, err)
		consumer.wait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
		err = consumer.Run(ctx)
		cancel()
		if verified {
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, 1, fetches)
		} else {
			require.ErrorIs(t, err, ErrQuarantineSource)
			require.Zero(t, fetches)
		}
		require.Zero(t, stores)
		require.Zero(t, acks)
	}
}

func TestSerialAdmissionPreparedAckFailure(t *testing.T) {
	fetches, prepares := 0, 0
	message := admissionFixtureMessage(t, 42)
	message.Acknowledge = func(context.Context) error { return ErrAssignmentLost }
	reader := admissionReaderFixture{verify: func(context.Context) error { return nil }, fetch: func(context.Context) (AdmissionMessage, error) { fetches++; return message, nil }}
	preparer := prepareFixture(func(context.Context, kafka.ScopedSendRequested) (PreparationResult, error) {
		prepares++
		return PreparationResult{SnapshotComplete: true}, nil
	})
	quarantine := quarantineFixture(func(context.Context, SourceMessage, QuarantineReason, int32) error {
		t.Fatal("commit failure after durable preparation must not quarantine")
		return nil
	})
	consumer, err := NewSerialConsumer(reader, preparer, quarantine)
	require.NoError(t, err)
	require.ErrorIs(t, consumer.Run(context.Background()), ErrAssignmentLost)
	require.Equal(t, 1, fetches)
	require.Equal(t, 1, prepares)
}
