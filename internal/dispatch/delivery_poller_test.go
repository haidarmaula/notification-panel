package dispatch

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/fcm"
)

func pollID(id byte) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte{15: id}, Valid: true}
}

type pollTestLedger struct {
	app        pgtype.UUID
	devices    [][]byte
	record     func(context.Context, *SubmissionResult) (RecordedResult, error)
	deferUntil func(pgtype.UUID, time.Time) error
	renew      func(context.Context) error
	release    func()
	done       bool
	failID     byte
}

func (l *pollTestLedger) discoverCampaigns(_ context.Context, app pgtype.UUID, after, through int64, limit int32) ([]RecoveryCampaign, error) {
	if app != l.app {
		return nil, errors.New("incorrect application filter")
	}
	var out []RecoveryCampaign
	if through == 0 {
		through = int64(len(l.devices))
	}
	for id := after + 1; id <= through && len(out) < int(limit); id++ {
		out = append(out, RecoveryCampaign{NotificationID: id, ApplicationID: app, passEnd: through})
	}
	return out, nil
}
func (*pollTestLedger) AcquireDiscovered(_ context.Context, c RecoveryCampaign) (*ExecutionLease, error) {
	return &ExecutionLease{application: c.ApplicationID, notificationID: c.NotificationID}, nil
}
func (l *pollTestLedger) RecoverPage(_ context.Context, lease *ExecutionLease, after pgtype.UUID, limit int32) (DeviceRecoveryPage, error) {
	page := DeviceRecoveryPage{After: after}
	for _, id := range l.devices[lease.notificationID-1] {
		if after.Valid && id <= after.Bytes[15] {
			continue
		}
		if len(page.Ready) == int(limit) {
			page.More = true
			break
		}
		if id == l.failID {
			page.failedDevice = pollID(id)
			return page, errRetirementFixture
		}
		page.After = pollID(id)
		page.Ready = append(page.Ready, page.After)
	}
	return page, nil
}

var errRetirementFixture = errors.New("outbox rejected retirement")

func TestDeliveryPollerVisitsHealthyDevicesAroundPersistentRetirementFailure(t *testing.T) {
	for _, bad := range []byte{1, 2} {
		good := 3 - bad
		ledger := &pollTestLedger{app: pollID(20), devices: [][]byte{{1, 2}, {3, 4}}, failID: bad}
		visits, failures, other := 0, 0, 0
		p := &DeliveryPoller{ledger: ledger, policy: DeliveryPollPolicy{
			ApplicationID: ledger.app, CampaignPageSize: 2, DevicePageSize: 2, Concurrency: 1, RenewInterval: time.Minute,
		}, send: func(_ context.Context, lease *ExecutionLease, id pgtype.UUID) (*SubmissionResult, error) {
			switch {
			case id == pollID(bad):
				t.Fatal("attempted the device with a failed retirement")
			case id == pollID(good):
				visits++
			case lease.notificationID == 2:
				other++
			}
			// Leave healthy devices due so repeated sweeps exercise both orders.
			return nil, ErrExecutionHeld
		}}
		for range 16 {
			_, err := p.Tick(t.Context())
			if errors.Is(err, errRetirementFixture) {
				failures++
			} else if err != nil {
				t.Fatal(err)
			}
		}
		if visits < 2 || other < 2 || failures < 2 {
			t.Fatalf("bad device %d: visits=%d other campaign=%d surfaced failures=%d", bad, visits, other, failures)
		}
	}
}
func (l *pollTestLedger) RecordResult(ctx context.Context, result *SubmissionResult) (RecordedResult, error) {
	return l.record(ctx, result)
}
func (l *pollTestLedger) deferDelivery(_ context.Context, _ *ExecutionLease, id pgtype.UUID, retryAt time.Time) error {
	return l.deferUntil(id, retryAt)
}
func (l *pollTestLedger) Renew(ctx context.Context, _ *ExecutionLease) error { return l.renew(ctx) }
func (l *pollTestLedger) Release(_ context.Context, _ *ExecutionLease) error {
	if l.release != nil {
		l.release()
	}
	return nil
}
func (l *pollTestLedger) FinalizeCampaign(context.Context, *ExecutionLease) (bool, error) {
	return l.done, nil
}

func TestDeliveryPollerAdvancesPastHeldDevicesAndCampaigns(t *testing.T) {
	ledger := &pollTestLedger{app: pollID(20), devices: [][]byte{{1, 2, 3}, {4, 5}}}
	var visits [2][]byte
	p := &DeliveryPoller{ledger: ledger, policy: DeliveryPollPolicy{ApplicationID: ledger.app, CampaignPageSize: 1, DevicePageSize: 1, Concurrency: 1, RenewInterval: time.Minute},
		send: func(_ context.Context, lease *ExecutionLease, id pgtype.UUID) (*SubmissionResult, error) {
			visits[lease.notificationID-1] = append(visits[lease.notificationID-1], id.Bytes[15])
			return nil, ErrExecutionHeld
		},
	}
	for range 24 {
		r, err := p.Tick(t.Context())
		if err != nil || r.Acquired > 1 || r.Held > 1 {
			t.Fatalf("tick: %+v %v", r, err)
		}
		// Continual arrivals cannot postpone completing a started pass. These
		// campaigns have no ready devices, keeping visit recording focused.
		ledger.devices = append(ledger.devices, nil)
	}
	for i, devices := range ledger.devices[:2] {
		for _, id := range devices {
			if !slices.Contains(visits[i], id) {
				t.Fatalf("never reached campaign %d, device %d: %v", i+1, id, visits)
			}
		}
	}
}

// A new campaign with devices above the current cursor arrives every tick.
// A held low device must get another turn after each finite device sweep.
func TestDeliveryPollerWrapsDeviceCursorUnderContinuousArrivals(t *testing.T) {
	ledger := &pollTestLedger{app: pollID(20), devices: [][]byte{{1, 2}}}
	visits := 0
	p := &DeliveryPoller{ledger: ledger, policy: DeliveryPollPolicy{
		ApplicationID: ledger.app, CampaignPageSize: 100, DevicePageSize: 1, Concurrency: 1, RenewInterval: time.Minute,
	}, send: func(_ context.Context, lease *ExecutionLease, id pgtype.UUID) (*SubmissionResult, error) {
		if lease.notificationID == 1 && id == pollID(1) {
			visits++
		}
		return nil, ErrExecutionHeld
	}}
	for i := range byte(12) {
		if _, err := p.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
		ledger.devices = append(ledger.devices, []byte{3 + 2*i, 4 + 2*i})
	}
	if visits < 2 {
		t.Fatalf("low device visited %d times", visits)
	}
}

func TestDeliveryPollerReconcilesSameEvidenceWithoutResending(t *testing.T) {
	result := new(SubmissionResult)
	var sends, records int
	ledger := &pollTestLedger{app: pollID(20), devices: [][]byte{{1}}, done: true,
		record: func(ctx context.Context, r *SubmissionResult) (RecordedResult, error) {
			if r != result || ctx.Err() != nil {
				t.Error("discarded submitted evidence")
			}
			records++
			if records <= 2 {
				return RecordedResult{}, errors.New("commit acknowledgement lost")
			}
			return RecordedResult{AlreadyRecorded: true}, nil
		},
	}
	p := &DeliveryPoller{ledger: ledger, policy: DeliveryPollPolicy{ApplicationID: ledger.app, CampaignPageSize: 1, DevicePageSize: 5, Concurrency: 1, RenewInterval: time.Minute},
		send: func(context.Context, *ExecutionLease, pgtype.UUID) (*SubmissionResult, error) {
			sends++
			return result, ErrExecutionUnavailable
		},
	}
	r, err := p.Tick(t.Context())
	if err != nil || sends != 1 || records != 3 || r.Recorded != 1 || r.Unavailable != 1 || r.Finalized != 1 {
		t.Fatalf("tick: %+v %v sends=%d records=%d", r, err, sends, records)
	}
}

func TestDeliveryPollerPersistsPreclaimDeferral(t *testing.T) {
	until := time.Now().Add(time.Hour)
	calls := 0
	ledger := &pollTestLedger{
		app: pollID(20), devices: [][]byte{{1}},
		deferUntil: func(id pgtype.UUID, at time.Time) error {
			if id != pollID(1) || !at.Equal(until) {
				t.Errorf("incorrectly scoped or shortened deferral: id=%v until=%v", id, at)
			}
			calls++
			return nil
		},
	}
	p := &DeliveryPoller{ledger: ledger, policy: DeliveryPollPolicy{
		ApplicationID: ledger.app, CampaignPageSize: 1, DevicePageSize: 10, Concurrency: 1, RenewInterval: time.Minute,
	}, send: func(context.Context, *ExecutionLease, pgtype.UUID) (*SubmissionResult, error) {
		return nil, &fcm.Deferred{RetryAt: until}
	}}
	r, err := p.Tick(t.Context())
	if err != nil || r.Deferred != 1 || calls != 1 || r.Recorded != 0 {
		t.Fatalf("tick: %+v %v deferrals=%d", r, err, calls)
	}
}

func TestDeliveryPollerRenewalFailureCancelsAndJoins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var active, started, recorded atomic.Int32
		lost := errors.New("renewal failed")
		released := false
		ledger := &pollTestLedger{app: pollID(20), devices: [][]byte{{1, 2, 3}},
			renew: func(context.Context) error { return lost },
			record: func(ctx context.Context, _ *SubmissionResult) (RecordedResult, error) {
				if ctx.Err() != nil {
					t.Error("cancelled persistence")
				}
				recorded.Add(1)
				return RecordedResult{}, nil
			},
			release: func() {
				if active.Load() != 0 || recorded.Load() != 2 {
					t.Error("released before workers joined")
				}
				released = true
			},
		}
		p := &DeliveryPoller{ledger: ledger, policy: DeliveryPollPolicy{ApplicationID: ledger.app, CampaignPageSize: 1, DevicePageSize: 3, Concurrency: 2, RenewInterval: time.Second},
			send: func(ctx context.Context, _ *ExecutionLease, _ pgtype.UUID) (*SubmissionResult, error) {
				active.Add(1)
				defer active.Add(-1)
				started.Add(1)
				<-ctx.Done()
				return new(SubmissionResult), context.Cause(ctx)
			},
		}
		r, err := p.Tick(t.Context())
		if !errors.Is(err, lost) || !released || started.Load() != 2 || r.Recorded != 2 {
			t.Fatalf("tick: %+v %v released=%v started=%v", r, err, released, started.Load())
		}
	})
}

func TestDeliveryPollerStopsResultPersistenceOnFenceOrDeadline(t *testing.T) {
	for _, cause := range []error{ErrExecutionHeld, ErrExecutionUnavailable, errors.New("result database down")} {
		t.Run(cause.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				known := new(SubmissionResult)
				ledger := &pollTestLedger{record: func(_ context.Context, result *SubmissionResult) (RecordedResult, error) {
					if result != known {
						t.Fatal("changed result while reconciling")
					}
					calls++
					return RecordedResult{}, cause
				}}
				poller := &DeliveryPoller{ledger: ledger}
				started := time.Now()
				err := poller.persist(t.Context(), known)
				if !errors.Is(err, cause) {
					t.Fatalf("lost failure: %v", err)
				}
				if cause == ErrExecutionHeld || cause == ErrExecutionUnavailable {
					if calls != 1 || time.Since(started) != 0 {
						t.Fatalf("retried after fence: %d calls", calls)
					}
				} else if calls < 3 || calls > 20 || time.Since(started) != 10*time.Second {
					t.Fatalf("unbounded or premature result retry: %d calls in %v", calls, time.Since(started))
				}
			})
		})
	}
}
