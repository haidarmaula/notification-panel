package dispatch

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"hello/internal/database/sqlc"
	"hello/internal/kafka"
)

// RecoveryReport counts attempted candidates, retaining progress on partial failure.
// Prepared means a known snapshot; Terminal means no snapshot was established.
type RecoveryReport struct {
	Visited  int
	Prepared int
	Terminal int
	Held     int
	Rejected int
}

// QueuedRecovery discovers current-lineage QUEUED campaigns for bounded preparation.
// It does not publish messages or recover PROCESSING provider work.
type QueuedRecovery struct {
	q         *sqlc.Queries
	preparer  *Preparer
	busy      atomic.Bool
	afterID   int64
	throughID int64
}

// NewQueuedRecovery requires discovery queries and the canonical campaign preparer.
func NewQueuedRecovery(q *sqlc.Queries, preparer *Preparer) (*QueuedRecovery, error) {
	if q == nil || preparer == nil {
		return nil, errors.New("queued recovery requires queries and preparer")
	}
	return &QueuedRecovery{q: q, preparer: preparer}, nil
}

// Tick discovers metadata without child locks, then prepares each campaign in
// its own canonical transaction. Returned counts retain committed progress if
// a later candidate fails. The caller must back off on errors before polling.
func (r *QueuedRecovery) Tick(ctx context.Context, pageSize int32) (RecoveryReport, error) {
	var report RecoveryReport
	if pageSize < 1 || pageSize > 100 {
		return report, errors.New("recovery page size must be between 1 and 100")
	}
	if !r.busy.CompareAndSwap(false, true) {
		return report, errors.New("queued recovery tick already running")
	}
	defer r.busy.Store(false)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := r.q.ListRecoverableQueuedCampaigns(ctx, sqlc.ListRecoverableQueuedCampaignsParams{
		AfterID: r.afterID, ThroughID: r.throughID, PageSize: pageSize,
	})
	if err != nil {
		return report, err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		// Advance before attempting the transaction so a rolled-back head
		// cannot consume every tick. The finite pass revisits it on wrap.
		r.afterID, r.throughID = row.ID, row.PassEnd
		event := kafka.ScopedSendRequested{SchemaVersion: 1, ApplicationID: row.ApplicationPublicID.String(),
			NotificationID: row.ID, RequestedAt: row.QueuedAt.Time, DeadlineAt: row.DeadlineAt.Time,
			OriginEpoch: row.OriginEpoch.String(), SourceEpoch: row.SourceEpoch.String()}
		if row.ScheduledAt.Valid {
			at := row.ScheduledAt.Time
			event.ScheduledFor = &at
		}
		result, err := r.preparer.Prepare(ctx, event)
		report.Visited++
		switch {
		case errors.Is(err, ErrAdmissionHeld):
			report.Held++
		case errors.Is(err, ErrAdmissionEnvelope), errors.Is(err, ErrAdmissionTarget), errors.Is(err, ErrAdmissionState):
			report.Rejected++
		case err != nil:
			return report, err
		case result.SnapshotComplete:
			report.Prepared++
		default:
			report.Terminal++
		}
	}
	if len(rows) < int(pageSize) || r.afterID >= r.throughID {
		r.afterID, r.throughID = 0, 0
	}
	return report, nil
}
