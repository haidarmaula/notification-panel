package applications

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
)

// PromoteSchedule is a bounded internal scheduler transaction. False means no
// state change. It performs no network I/O and preserves the scheduled deadline.
func (s *Service) PromoteSchedule(ctx context.Context, project pgtype.UUID, campaignID int64) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	changed := false
	err := s.write(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		changed = false
		runtime, err := q.LockNotificationRuntime(ctx)
		if err != nil {
			return err
		}
		if runtime.Sealed {
			return errSendHeld
		}
		app, err := repository.NewApplicationRepository(q).LockByPublicID(ctx, project)
		if err != nil {
			return appError(err)
		}
		campaign, err := q.LockCampaignAcceptance(ctx, sqlc.LockCampaignAcceptanceParams{ApplicationID: app.ID, CampaignID: campaignID})
		if err != nil {
			return campaignLookupError(err)
		}
		if campaign.Status != "SCHEDULED" || !campaign.Due {
			return nil
		}
		if !campaign.DeadlineAt.Valid || !campaign.OriginEpoch.Valid || !campaign.SourceEpoch.Valid ||
			campaign.OriginEpoch != runtime.OriginEpoch || campaign.SourceEpoch != runtime.SourceEpoch {
			return errSendHeld
		}
		if campaign.Expired {
			n, err := q.ExpireScopedSchedule(ctx, sqlc.ExpireScopedScheduleParams{ApplicationID: app.ID, CampaignID: campaignID})
			changed = n == 1
			return err
		}
		row, err := q.PromoteScopedCampaign(ctx, sqlc.PromoteScopedCampaignParams{ApplicationID: app.ID, CampaignID: campaignID})
		if err != nil {
			return err
		}
		if err := q.InsertSendOutbox(ctx, sqlc.InsertSendOutboxParams{ApplicationID: app.ID, NotificationID: campaignID,
			RequestedAt: row.QueuedAt, ScheduledFor: row.ScheduledAt, DeadlineAt: row.DeadlineAt,
			OriginEpoch: row.OriginEpoch, SourceEpoch: row.SourceEpoch}); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed && err == nil, err
}
