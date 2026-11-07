package dispatch

import (
	"context"

	"hello/internal/database/sqlc"
)

// FinalizeCampaign closes a complete campaign under its live lease. It returns
// false while deliveries are pending or a current attempt can still record its
// outcome after an early receipt. Only a confirmed commit returns true.
func (s *Execution) FinalizeCampaign(ctx context.Context, lease *ExecutionLease) (bool, error) {
	if !s.owns(lease) {
		return false, ErrExecutionInput
	}
	var finalized bool
	err := s.transaction(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		// A dispatch hold can stop new sends while their durable outcomes still
		// need aggregation. Runtime sealing and both lineage fences still apply.
		if err := s.lockResultBase(ctx, q, lease, false); err != nil {
			return err
		}
		runtime := sqlc.LockNotificationRuntimeRow{OriginEpoch: lease.origin, SourceEpoch: lease.source}
		if err := lockExecutionCampaign(ctx, q, lease.applicationID, lease.notificationID, runtime); err != nil {
			return err
		}
		counts, err := q.CountExecutionOutcomes(ctx, sqlc.CountExecutionOutcomesParams{
			ApplicationID: lease.applicationID, NotificationID: lease.notificationID,
			OriginEpoch: lease.origin, SourceEpoch: lease.source, Owner: s.owner, Generation: lease.generation,
		})
		if err != nil {
			return executionMissing(err)
		}
		if counts.DeviceCount != counts.EligibleDeviceCount {
			return ErrExecutionHeld
		}
		if counts.Unfinished > 0 {
			return nil
		}
		completed := counts.DeviceCount == 0 || counts.Successes > 0
		affected, err := q.FinalizeExecutionCampaign(ctx, sqlc.FinalizeExecutionCampaignParams{
			Completed: completed, ApplicationID: lease.applicationID, NotificationID: lease.notificationID,
			OriginEpoch: lease.origin, SourceEpoch: lease.source, Owner: s.owner, Generation: lease.generation,
		})
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrExecutionUnavailable
		}
		finalized = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return finalized, nil
}
