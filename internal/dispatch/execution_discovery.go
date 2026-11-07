package dispatch

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

// RecoveryCampaign is an advisory scan result. Acquisition must recheck its
// original lineage; discovery never grants permission to send.
type RecoveryCampaign struct {
	ApplicationID  pgtype.UUID
	NotificationID int64
	origin, source pgtype.UUID
	passEnd        int64
}

// DiscoverCampaigns scans unowned, complete PROCESSING campaigns in stable ID
// order. Start at zero; pass the last visited ID to continue, and wrap at the
// end on the next poll. Holds suppress new sends while deadlines continue.
func (s *Execution) DiscoverCampaigns(ctx context.Context, after int64, limit int32) ([]RecoveryCampaign, error) {
	return s.discoverCampaigns(ctx, pgtype.UUID{}, after, 0, limit)
}

func (s *Execution) discoverCampaigns(ctx context.Context, application pgtype.UUID, after, through int64, limit int32) ([]RecoveryCampaign, error) {
	if s == nil || s.db == nil || after < 0 || limit < 1 || limit > 100 {
		return nil, ErrExecutionInput
	}
	var page []RecoveryCampaign
	err := s.transaction(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		rows, err := q.ListExecutionRecoveryCampaigns(ctx, sqlc.ListExecutionRecoveryCampaignsParams{
			ApplicationPublicID: application, AfterID: after, ThroughID: through, PageSize: limit, MaxAttempts: s.policy.MaxAttempts,
		})
		if err != nil {
			return err
		}
		for _, row := range rows {
			page = append(page, RecoveryCampaign{ApplicationID: row.ApplicationPublicID, NotificationID: row.ID,
				origin: row.OriginEpoch, source: row.SourceEpoch, passEnd: row.PassEnd})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

// AcquireDiscovered obtains a fresh generation for this scan result. A lease
// acquired under an application or dispatch hold can only do bookkeeping, even
// if the hold is later lifted. A racing acquisition is an ordinary miss.
func (s *Execution) AcquireDiscovered(ctx context.Context, campaign RecoveryCampaign) (*ExecutionLease, error) {
	if !executionUUID(campaign.origin) || !executionUUID(campaign.source) {
		return nil, ErrExecutionInput
	}
	return s.acquire(ctx, campaign.ApplicationID, campaign.NotificationID, &campaign)
}

// DeviceRecoveryPage reports committed retirements and advisory IDs for normal
// permit-gated submission. After is the last visited ID, including on partial
// failure. More refers to this pass; another poll will pick up newly due work.
type DeviceRecoveryPage struct {
	Retired           int
	Ready             []pgtype.UUID
	After             pgtype.UUID
	More              bool
	CampaignFinalized bool
	// A polled sweep may skip a failing row until the next wrap. After
	// remains the last successful visit for callers retrying the same page.
	failedDevice pgtype.UUID
}

// RecoverPage retires no-send work in bounded, independent transactions. Start
// with an invalid UUID cursor. A caller may submit Ready devices only through
// the normal submission path. Renew while working; finalize after recording the
// final results, and release any still-active lease when leaving the campaign.
func (s *Execution) RecoverPage(ctx context.Context, lease *ExecutionLease, after pgtype.UUID, limit int32) (DeviceRecoveryPage, error) {
	page := DeviceRecoveryPage{After: after}
	if !s.owns(lease) || limit < 1 || limit > 100 {
		return page, ErrExecutionInput
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var rows []sqlc.ListExecutionRecoveryDevicesRow
	var sendAllowed bool
	err := s.transaction(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		app, runtime, allowed, err := s.lockScope(ctx, q, lease.application)
		if err != nil {
			return err
		}
		if app != lease.applicationID || runtime.OriginEpoch != lease.origin || runtime.SourceEpoch != lease.source {
			return ErrExecutionHeld
		}
		sendAllowed = allowed && !lease.bookkeepingOnly
		rows, err = q.ListExecutionRecoveryDevices(ctx, sqlc.ListExecutionRecoveryDevicesParams{
			ApplicationID: app, NotificationID: lease.notificationID, Generation: lease.generation,
			Owner: s.owner, OriginEpoch: lease.origin, SourceEpoch: lease.source, AfterID: after,
			SendAllowed: sendAllowed, PageSize: limit + 1, MaxAttempts: s.policy.MaxAttempts,
		})
		return err
	})
	if err != nil {
		return page, err
	}
	page.More = len(rows) > int(limit)
	if page.More {
		rows = rows[:limit]
	}
	for _, row := range rows {
		if row.NeedsRecovery {
			retired, err := s.RecoverDelivery(ctx, lease, row.ID)
			if err != nil {
				page.failedDevice = row.ID
				return page, err
			}
			if retired {
				page.Retired++
			}
		} else if sendAllowed {
			page.Ready = append(page.Ready, row.ID)
		}
		page.After = row.ID
	}
	if !page.More && len(page.Ready) == 0 {
		page.CampaignFinalized, err = s.FinalizeCampaign(ctx, lease)
	}
	return page, err
}
