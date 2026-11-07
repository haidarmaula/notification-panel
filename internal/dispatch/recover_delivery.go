package dispatch

import (
	"context"
	"math"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

// RecoverDelivery fails a pending delivery that can no longer be submitted.
// A true result acknowledges both the ledger change and its outbox event.
// Current unresolved attempts remain eligible to record their real outcome.
func (s *Execution) RecoverDelivery(ctx context.Context, lease *ExecutionLease, deliveryID pgtype.UUID) (bool, error) {
	if !s.owns(lease) || !executionUUID(deliveryID) {
		return false, ErrExecutionInput
	}
	var changed bool
	err := s.transaction(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		if err := s.lockResultBase(ctx, q, lease, false); err != nil {
			return err
		}
		// The intent's immutable identity locates the original user. Lock that
		// user even when inactive so a concurrent reactivation cannot race us.
		identity, err := q.LockRecoveryDeliveryUser(ctx, sqlc.LockRecoveryDeliveryUserParams{
			ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: deliveryID,
		})
		if err != nil {
			return executionMissing(err)
		}
		runtime := sqlc.LockNotificationRuntimeRow{OriginEpoch: lease.origin, SourceEpoch: lease.source}
		if err := lockExecutionCampaign(ctx, q, lease.applicationID, lease.notificationID, runtime); err != nil {
			return err
		}
		installation, err := q.LockRecoveryInstallationOwner(ctx, sqlc.LockRecoveryInstallationOwnerParams{
			ApplicationID: lease.applicationID, InstallationID: identity.InstallationID,
		})
		if err != nil {
			return executionMissing(err)
		}
		retired, err := q.LockRecoveryBindingRetirement(ctx, sqlc.LockRecoveryBindingRetirementParams{
			ApplicationID: lease.applicationID, BindingID: identity.BindingID,
			InstallationID: identity.InstallationID, OwnerUserID: identity.OwnerUserID, OwnerEpoch: identity.OwnerEpoch,
		})
		if err != nil {
			return executionMissing(err)
		}
		delivery, err := q.LockRecoverableDelivery(ctx, sqlc.LockRecoverableDeliveryParams{
			ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: deliveryID,
			Owner: s.owner, Generation: lease.generation, OriginEpoch: lease.origin, SourceEpoch: lease.source,
		})
		if err != nil {
			return executionMissing(err)
		}
		reason := recoveryReason(delivery, identity, installation, retired.Valid, lease.generation, s.policy.MaxAttempts)
		if reason == "" {
			return nil
		}
		if delivery.DeliveryVersion == math.MaxInt64 {
			return ErrExecutionHeld
		}
		updated, err := q.FailRecoveredDelivery(ctx, sqlc.FailRecoveredDeliveryParams{
			ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: deliveryID,
			Owner: s.owner, Generation: lease.generation, OriginEpoch: lease.origin, SourceEpoch: lease.source,
			Reason: reason, RecordedAt: delivery.RecordedAt,
		})
		if err != nil {
			return executionMissing(err)
		}
		if err := enqueueDeliveryUpdate(ctx, q, lease.deliverySource(), "submission", updated, delivery.RecordedAt); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

func recoveryReason(delivery sqlc.LockRecoverableDeliveryRow, identity sqlc.LockRecoveryDeliveryUserRow,
	installation sqlc.LockRecoveryInstallationOwnerRow, retired bool, generation int64, maxAttempts int32,
) string {
	if delivery.Status != "PENDING" {
		return ""
	}
	// A charged attempt may still produce a result after its deadline, even on
	// the final charge. Only a later generation can retire an unresolved attempt.
	if delivery.AttemptID.Valid && !delivery.AttemptResolvedAt.Valid && delivery.ClaimGeneration.Int64 >= generation {
		return ""
	}
	switch {
	case !delivery.RecordedAt.Time.Before(delivery.DeadlineAt.Time), !delivery.NextAttemptAt.Time.Before(delivery.DeadlineAt.Time):
		return "ttl_expired"
	case delivery.AttemptCount >= maxAttempts:
		return "retry_budget_exhausted"
	case identity.UserStatus != "ACTIVE", !installation.OwnerUserID.Valid:
		return "device_inactive"
	case retired, installation.OwnerUserID.Int64 != identity.OwnerUserID, installation.OwnerEpoch != identity.OwnerEpoch:
		return "token_changed"
	default:
		return ""
	}
}
