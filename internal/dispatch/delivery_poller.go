package dispatch

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
	"hello/internal/fcm"
)

// DeliveryPollPolicy bounds the campaigns and devices visited by each Tick.
// Concurrency is per process and must fit the configured FCM allocation.
type DeliveryPollPolicy struct {
	ApplicationID                    pgtype.UUID
	CampaignPageSize, DevicePageSize int32
	Concurrency                      int
	RenewInterval                    time.Duration
}

// DeliveryPollReport counts durable changes only after an acknowledged commit.
// Recorded includes reconciled commits. Held, Deferred and Unavailable count
// operations which could not proceed, including after an advisory discovery.
type DeliveryPollReport struct {
	Visited, Acquired, Finalized int
	Retired, Recorded            int
	Held, Deferred, Unavailable  int
}

type deliveryLedger interface {
	discoverCampaigns(context.Context, pgtype.UUID, int64, int64, int32) ([]RecoveryCampaign, error)
	AcquireDiscovered(context.Context, RecoveryCampaign) (*ExecutionLease, error)
	deferDelivery(context.Context, *ExecutionLease, pgtype.UUID, time.Time) error
	RecoverPage(context.Context, *ExecutionLease, pgtype.UUID, int32) (DeviceRecoveryPage, error)
	RecordResult(context.Context, *SubmissionResult) (RecordedResult, error)
	FinalizeCampaign(context.Context, *ExecutionLease) (bool, error)
	Renew(context.Context, *ExecutionLease) error
	Release(context.Context, *ExecutionLease) error
}

// DeliveryPoller visits one application in bounded pages. Its owner schedules
// non-overlapping ticks and joins them before closing the FCM client or database.
type DeliveryPoller struct {
	ledger                         deliveryLedger
	send                           func(context.Context, *ExecutionLease, pgtype.UUID) (*SubmissionResult, error)
	policy                         DeliveryPollPolicy
	busy                           atomic.Bool
	afterCampaign, throughCampaign int64
	afterDevice, nextRound         pgtype.UUID
}

// NewDeliveryPoller binds the sender to its own execution service and app scope.
func NewDeliveryPoller(execution *Execution, submitter *Submitter, policy DeliveryPollPolicy) (*DeliveryPoller, error) {
	if execution == nil || execution.db == nil || submitter == nil || submitter.execution != execution ||
		!executionUUID(policy.ApplicationID) || policy.CampaignPageSize < 1 || policy.CampaignPageSize > 100 ||
		policy.DevicePageSize < 1 || policy.DevicePageSize > 100 || policy.Concurrency < 1 || policy.Concurrency > 128 ||
		policy.RenewInterval < time.Second || policy.RenewInterval > execution.policy.LeaseDuration/3 {
		return nil, ErrExecutionInput
	}
	return &DeliveryPoller{ledger: execution, send: submitter.Send, policy: policy}, nil
}

// Tick gives each discovered campaign at most one device page per round. It
// retains cursor progress across ticks and returns acknowledged work on errors.
func (p *DeliveryPoller) Tick(ctx context.Context) (DeliveryPollReport, error) {
	var report DeliveryPollReport
	if p == nil || p.ledger == nil || !p.busy.CompareAndSwap(false, true) {
		return report, ErrExecutionInput
	}
	defer p.busy.Store(false)
	campaigns, err := p.ledger.discoverCampaigns(ctx, p.policy.ApplicationID, p.afterCampaign, p.throughCampaign, p.policy.CampaignPageSize)
	if err != nil {
		return report, err
	}
	for _, campaign := range campaigns {
		if ctx.Err() != nil {
			return report, context.Cause(ctx)
		}
		report.Visited++
		p.afterCampaign = campaign.NotificationID
		// Keep the same campaign horizon until the device cursor wraps.
		// Otherwise new campaigns can keep introducing higher device IDs
		// while deferred lower IDs wait forever.
		p.throughCampaign = campaign.passEnd
		if campaign.ApplicationID != p.policy.ApplicationID {
			return report, ErrExecutionHeld
		}
		lease, err := p.ledger.AcquireDiscovered(ctx, campaign)
		if err == nil {
			report.Acquired++
			var page DeviceRecoveryPage
			page, err = p.workCampaign(ctx, lease, &report)
			// Start the next round no higher than the smallest unfinished
			// page end. Later UUIDs advance even if earlier sends stay held.
			next := pgtype.UUID{}
			if page.failedDevice.Valid {
				next = page.failedDevice
			} else if page.More {
				next = page.After
			}
			if next.Valid && (!p.nextRound.Valid || bytes.Compare(next.Bytes[:], p.nextRound.Bytes[:]) < 0) {
				p.nextRound = next
			}
		}
		if err != nil && !report.countSkip(err) {
			return report, err
		}
	}
	if len(campaigns) < int(p.policy.CampaignPageSize) || p.afterCampaign >= p.throughCampaign {
		p.afterCampaign = 0
		p.afterDevice, p.nextRound = p.nextRound, pgtype.UUID{}
		if !p.afterDevice.Valid {
			p.throughCampaign = 0
		}
	}
	return report, nil
}

func (p *DeliveryPoller) workCampaign(parent context.Context, lease *ExecutionLease, report *DeliveryPollReport) (DeviceRecoveryPage, error) {
	ctx, cancel := context.WithCancelCause(parent)
	joined := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(p.policy.RenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				joined <- nil
				return
			case <-ticker.C:
				if err := p.ledger.Renew(ctx, lease); err != nil {
					if ctx.Err() != nil {
						joined <- nil
						return
					}
					cancel(err)
					joined <- err
					return
				}
			}
		}
	}()
	page, err := p.ledger.RecoverPage(ctx, lease, p.afterDevice, p.policy.DevicePageSize)
	report.Retired += page.Retired
	// A failed retirement can follow healthy devices in the same page. Each
	// send obtains its own permit and rechecks the live lease and eligibility.
	if len(page.Ready) != 0 && ctx.Err() == nil && (err == nil || page.failedDevice.Valid) {
		sendErr := p.sendPage(ctx, lease, page.Ready, report)
		if sendErr != nil && (err == nil || report.countSkip(err)) {
			err = sendErr
		}
	}
	cancel(nil)
	renewErr := <-joined
	if page.CampaignFinalized {
		// RecoverPage can close the campaign just before a renewal misses it.
		report.Finalized++
		return page, err
	}
	if renewErr != nil && (err == nil || errors.Is(err, context.Canceled) || report.countSkip(err)) {
		err = renewErr
	}
	if err == nil && !page.More && len(page.Ready) != 0 {
		var done bool
		done, err = p.ledger.FinalizeCampaign(parent, lease)
		if done && err == nil {
			report.Finalized++
			return page, nil
		}
	}
	// All sends, result writes and renewal have joined. Release uses the DB's
	// live generation fence even when the caller's deadline has passed.
	cleanup, stop := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer stop()
	releaseErr := p.ledger.Release(cleanup, lease)
	if releaseErr != nil && !report.countSkip(releaseErr) {
		return page, releaseErr
	}
	return page, err
}

func (r *DeliveryPollReport) countSkip(err error) bool {
	switch {
	case errors.Is(err, ErrExecutionHeld):
		r.Held++
	case errors.Is(err, ErrExecutionUnavailable):
		r.Unavailable++
	case errors.Is(err, fcm.ErrDeferred):
		r.Deferred++
	default:
		return false
	}
	return true
}

func (p *DeliveryPoller) sendPage(parent context.Context, lease *ExecutionLease, ids []pgtype.UUID, report *DeliveryPollReport) error {
	ctx, stop := context.WithCancelCause(parent)
	defer stop(nil)
	workers := make([]DeliveryPollReport, min(p.policy.Concurrency, len(ids)))
	jobs := make(chan pgtype.UUID, len(ids))
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	var group sync.WaitGroup
	for i := range workers {
		group.Go(func() {
			for id := range jobs {
				if ctx.Err() != nil {
					return
				}
				result, sendErr := p.send(ctx, lease, id)
				var deferred *fcm.Deferred
				if result == nil && errors.As(sendErr, &deferred) {
					if err := p.ledger.deferDelivery(ctx, lease, id, deferred.RetryAt); err != nil {
						stop(err)
						return
					}
				}
				if result != nil {
					// Even a cancelled provider call may carry truthful evidence.
					// Record it while this campaign lease is still live.
					if err := p.persist(ctx, result); err != nil {
						stop(err)
						return
					}
					workers[i].Recorded++
				}
				if sendErr != nil && !workers[i].countSkip(sendErr) {
					stop(sendErr)
					return
				}
			}
		})
	}
	group.Wait()
	for _, counts := range workers {
		report.Recorded += counts.Recorded
		report.Held += counts.Held
		report.Deferred += counts.Deferred
		report.Unavailable += counts.Unavailable
	}
	return context.Cause(ctx)
}

// deferDelivery preserves a pre-claim backoff across process restarts. This
// changes scheduling only. Attempt evidence, version and outbox are untouched.
func (s *Execution) deferDelivery(ctx context.Context, lease *ExecutionLease, id pgtype.UUID, retryAt time.Time) error {
	if !s.owns(lease) || !executionUUID(id) || retryAt.IsZero() {
		return ErrExecutionInput
	}
	at := resultTime(retryAt)
	if at.Time.Before(retryAt) {
		at.Time = at.Time.Add(time.Microsecond)
	}
	return s.transaction(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		if err := s.lockLeaseBase(ctx, q, lease); err != nil {
			return err
		}
		runtime := sqlc.LockNotificationRuntimeRow{OriginEpoch: lease.origin, SourceEpoch: lease.source}
		if err := lockExecutionCampaign(ctx, q, lease.applicationID, lease.notificationID, runtime); err != nil {
			return err
		}
		n, err := q.DeferUnchargedDelivery(ctx, sqlc.DeferUnchargedDeliveryParams{
			ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: id,
			Owner: s.owner, Generation: lease.generation, OriginEpoch: lease.origin, SourceEpoch: lease.source,
			RetryAt: at, MaxAttempts: s.policy.MaxAttempts,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrExecutionUnavailable
		}
		return nil
	})
}

func (p *DeliveryPoller) persist(parent context.Context, result *SubmissionResult) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	for delay := 50 * time.Millisecond; ; delay = min(2*delay, time.Second) {
		_, err := p.ledger.RecordResult(ctx, result)
		if err == nil || ctx.Err() != nil || errors.Is(err, ErrExecutionInput) || errors.Is(err, ErrExecutionHeld) || errors.Is(err, ErrExecutionUnavailable) {
			return err
		}
		// Reconcile the original evidence after a transient database error
		// or lost acknowledgement. Every write rechecks the exact attempt.
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
}
