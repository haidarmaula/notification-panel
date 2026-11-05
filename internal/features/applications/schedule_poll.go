package applications

import (
	"context"
	"errors"
	"time"
)

// PromotionReport retains acknowledged per-campaign progress on partial failure.
// Changed includes both due promotion and expiry; it is not a delivery count.
type PromotionReport struct {
	Visited int
	Changed int
	Held    int
}

// PromoteDueSchedules is an internal scheduler sweep, not a staff HTTP action.
// Discovery is nonlocking and restricted to unsealed current-lineage work. Each
// candidate rechecks state/epochs under the canonical promotion transaction,
// preserving accepted deadlines even during migration holds. A failed candidate
// stops this sweep; earlier committed progress is retained without batch retries.
func (s *Service) PromoteDueSchedules(ctx context.Context, pageSize int32) (PromotionReport, error) {
	var report PromotionReport
	if pageSize < 1 || pageSize > 100 {
		return report, errors.New("schedule page size must be between 1 and 100")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return report, err
	}
	discoveryCtx, stopDiscovery := context.WithTimeout(ctx, 5*time.Second)
	rows, err := s.q.ListDueScopedSchedules(discoveryCtx, pageSize)
	stopDiscovery()
	if err != nil {
		return report, err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return report, err
		}
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
	return report, nil
}
