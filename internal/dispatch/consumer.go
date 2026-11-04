package dispatch

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"hello/internal/kafka"
)

var ErrAssignmentLost = errors.New("send reader assignment lost")

// AdmissionMessage is supplied by a verified reader. Acknowledge MUST be bound
// to this delivery's assignment/generation and fail after revocation. Source
// buffers remain immutable and owned by this delivery until completion. It must
// honor context and never acknowledge other not-yet-prepared messages.
type AdmissionMessage struct {
	Source      SourceMessage
	Acknowledge func(context.Context) error
}

// AdmissionReader performs no auto-commit. VerifySource checks deployment source
// approval plus physical cluster/topic incarnation; Fetch rechecks on assignment
// changes. The caller owns
// reader shutdown and must not share a reader across coordinator instances.
type AdmissionReader interface {
	VerifySource(context.Context) error
	Fetch(context.Context) (AdmissionMessage, error)
}

type AdmissionPreparer interface {
	Prepare(context.Context, kafka.ScopedSendRequested) (PreparationResult, error)
}

type AdmissionQuarantine interface {
	Store(context.Context, SourceMessage, QuarantineReason, int32) error
}

type SerialConsumer struct {
	reader     AdmissionReader
	preparer   AdmissionPreparer
	quarantine AdmissionQuarantine
	wait       func(context.Context, time.Duration) error
}

func NewSerialConsumer(reader AdmissionReader, preparer AdmissionPreparer, quarantine AdmissionQuarantine) (*SerialConsumer, error) {
	if reader == nil || preparer == nil || quarantine == nil {
		return nil, errors.New("serial admission requires verified reader, preparer and quarantine")
	}
	return &SerialConsumer{reader: reader, preparer: preparer, quarantine: quarantine, wait: waitAdmission}, nil
}

func waitAdmission(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func admissionDelay(attempt int) time.Duration {
	base := time.Second * time.Duration(1<<min(max(attempt, 0), 4))
	return min(16*time.Second, base+time.Duration(rand.Int64N(int64(base/4)+1)))
}

func safePrepare(ctx context.Context, preparer AdmissionPreparer, event kafka.ScopedSendRequested) (result PreparationResult, err error) {
	defer func() {
		if recover() != nil {
			result = PreparationResult{}
			err = errors.New("admission preparation panicked")
		}
	}()
	return preparer.Prepare(ctx, event)
}

// Run fetches the next message only after this message's durable outcome and
// assignment-bound acknowledgement. On evidence failure it retains this message
// and retries until cancellation. On reader/ack failure it stops for recreation.
func (c *SerialConsumer) Run(ctx context.Context) error {
	verifyCtx, stopVerify := context.WithTimeout(ctx, 5*time.Second)
	err := c.reader.VerifySource(verifyCtx)
	stopVerify()
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		message, err := c.reader.Fetch(ctx)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if message.Acknowledge == nil {
			return errors.New("send reader did not provide assignment acknowledgement")
		}
		event, err := kafka.DecodeScopedSend(message.Source.Key, message.Source.Payload)
		reason, attempts := InvalidEnvelope, int32(1)
		if err == nil {
			reason, attempts, err = c.prepare(ctx, event)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			for failures := 0; ; failures = min(failures+1, 4) {
				storeCtx, stopStore := context.WithTimeout(ctx, 5*time.Second)
				err = c.quarantine.Store(storeCtx, message.Source, reason, attempts)
				stopStore()
				if err == nil {
					break
				}
				if err := c.wait(ctx, admissionDelay(min(failures, 4))); err != nil {
					return err
				}
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ackCtx, stopAck := context.WithTimeout(ctx, 5*time.Second)
		err = message.Acknowledge(ackCtx)
		stopAck()
		if err != nil {
			return err
		}
	}
}

func (c *SerialConsumer) prepare(ctx context.Context, event kafka.ScopedSendRequested) (QuarantineReason, int32, error) {
	budgetCtx, stop := context.WithTimeout(ctx, 90*time.Second)
	defer stop()
	reason := AdmissionExhausted
	var lastErr error
	var attempts int32
	for attempts < 3 {
		attempts++
		callCtx, stopCall := context.WithTimeout(budgetCtx, 30*time.Second)
		_, lastErr = safePrepare(callCtx, c.preparer, event)
		stopCall()
		if lastErr == nil {
			return "", attempts, nil
		}
		switch {
		case errors.Is(lastErr, ErrAdmissionEnvelope), errors.Is(lastErr, kafka.ErrScopedSendEnvelope):
			return AcceptanceMismatch, attempts, lastErr
		case errors.Is(lastErr, ErrAdmissionTarget):
			return UnsupportedTargets, attempts, lastErr
		case errors.Is(lastErr, ErrAdmissionState):
			return PreparationState, attempts, lastErr
		case errors.Is(lastErr, ErrAdmissionHeld):
			reason = PreparationHeld
		default:
			reason = AdmissionExhausted
		}
		if attempts == 3 || budgetCtx.Err() != nil {
			break
		}
		if err := c.wait(budgetCtx, admissionDelay(int(attempts-1))); err != nil {
			break
		}
	}
	return reason, attempts, lastErr
}
