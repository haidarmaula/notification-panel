package applications

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

// PromotionReport retains acknowledged per-campaign progress on partial failure.
// Changed includes both due promotion and expiry; it is not a delivery count.
type PromotionReport struct {
	Visited int
	Changed int
	Held    int
}

type scheduleCursor struct {
	busy      atomic.Bool
	afterTime pgtype.Timestamptz
	afterID   int64
	throughID int64
}

// PromoteDueSchedules is an internal scheduler sweep, not a staff HTTP action.
// Discovery is nonlocking and restricted to unsealed current-lineage work. Each
// candidate rechecks state/epochs under the canonical promotion transaction,
// preserving accepted deadlines even during migration holds. A failed candidate
// stops this sweep. The next sweep resumes after the failed candidate.
func (s *Service) PromoteDueSchedules(ctx context.Context, pageSize int32) (PromotionReport, error) {
	var report PromotionReport
	if pageSize < 1 || pageSize > 100 {
		return report, errors.New("schedule page size must be between 1 and 100")
	}
	cursor := &s.promotion
	if !cursor.busy.CompareAndSwap(false, true) {
		return report, errors.New("schedule promotion sweep already running")
	}
	defer cursor.busy.Store(false)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return report, err
	}
	discoveryCtx, stopDiscovery := context.WithTimeout(ctx, 5*time.Second)
	rows, err := s.q.ListDueScopedSchedules(discoveryCtx, sqlc.ListDueScopedSchedulesParams{
		PageSize: pageSize, AfterScheduledAt: cursor.afterTime, AfterID: cursor.afterID, ThroughID: cursor.throughID,
	})
	stopDiscovery()
	if err != nil {
		return report, err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		cursor.afterTime, cursor.afterID, cursor.throughID = row.ScheduledAt, row.ID, row.PassEnd
		changed, err := s.PromoteSchedule(ctx, row.ApplicationPublicID, row.ID)
		report.Visited++
		switch {
		case errors.Is(err, errSendHeld):
			report.Held++
		case err != nil:
			return report, err
		case changed:
			report.Changed++
		}
	}
	if len(rows) < int(pageSize) {
		cursor.afterTime, cursor.afterID, cursor.throughID = pgtype.Timestamptz{}, 0, 0
	}
	return report, nil
}
