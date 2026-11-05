package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
	"hello/internal/receipt"
)

var (
	ErrExecutionInput       = errors.New("invalid execution input or policy")
	ErrExecutionHeld        = errors.New("campaign execution is held")
	ErrExecutionUnavailable = errors.New("execution lease or delivery is unavailable")
)

// ExecutionPolicy bounds durable ownership and reservation. It does not enable
// provider execution or approve production rate, request timeout or freshness.
type ExecutionPolicy struct {
	LeaseDuration time.Duration
	RetryFloor    time.Duration
	MaxAttempts   int32
	Receipt       receipt.Policy
}

// Validate rejects unbounded/overflowing intervals before SQL second conversion.
func (p ExecutionPolicy) Validate() error {
	if p.LeaseDuration < 30*time.Second || p.LeaseDuration > 5*time.Minute || p.LeaseDuration%time.Second != 0 ||
		p.RetryFloor < 10*time.Second || p.RetryFloor > 24*time.Hour || p.RetryFloor%time.Second != 0 || p.MaxAttempts < 1 || p.MaxAttempts > 100 || p.Receipt.Validate() != nil {
		return ErrExecutionInput
	}
	return nil
}

// Execution owns one random process identity and immutable policy. Its operations
// make no provider calls and do not finalize deliveries or publish state updates.
type Execution struct {
	db     Transactor
	owner  pgtype.UUID
	policy ExecutionPolicy
}

// ExecutionLease is an opaque acknowledged-commit capability. It cannot be
// rebuilt from a database row or transferred to another Execution instance.
// Do not log the capability.
type ExecutionLease struct {
	service                                   *Execution
	application                               pgtype.UUID
	applicationID, notificationID, generation int64
	origin, source                            pgtype.UUID
}

// AttemptReservation is an opaque, acknowledged single-device charge. It grants
// no public send API; the future executor must enforce permits and consume it
// once, with a fresh lease/deadline/route check immediately before provider I/O.
// Do not log the reservation.
type AttemptReservation struct {
	lease                                              *ExecutionLease
	deliveryID, attemptID                              pgtype.UUID
	bindingID, installationID, ownerUserID, ownerEpoch int64
	material                                           receipt.Material
}

// NewExecution creates a distinct owner; restarts do not recover old capabilities.
func NewExecution(db Transactor, policy ExecutionPolicy) (*Execution, error) {
	if db == nil || policy.Validate() != nil {
		return nil, ErrExecutionInput
	}
	owner, err := newReplayClaimToken()
	if err != nil {
		return nil, err
	}
	return &Execution{db: db, owner: owner, policy: policy}, nil
}

// Acquire increments the campaign generation only after the previous lease is
// absent/expired and actionable pending work exists. Errors return no capability,
// including unknown commit acknowledgement. The accepted TTL is never extended.
func (s *Execution) Acquire(ctx context.Context, application pgtype.UUID, notificationID int64) (*ExecutionLease, error) {
	if s == nil || s.db == nil || !executionUUID(s.owner) || !executionUUID(application) || notificationID < 1 {
		return nil, ErrExecutionInput
	}
	var provisional *ExecutionLease
	err := s.transaction(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		appID, runtime, err := s.lockBase(ctx, q, application)
		if err != nil {
			return err
		}
		if err := lockExecutionCampaign(ctx, q, appID, notificationID, runtime); err != nil {
			return err
		}
		row, err := q.AcquireExecutionLease(ctx, sqlc.AcquireExecutionLeaseParams{
			ApplicationID: appID, NotificationID: notificationID, Owner: s.owner, OriginEpoch: runtime.OriginEpoch, SourceEpoch: runtime.SourceEpoch,
			LeaseSeconds: int32(s.policy.LeaseDuration / time.Second), MaxAttempts: s.policy.MaxAttempts,
		})
		if err != nil {
			return executionMissing(err)
		}
		provisional = &ExecutionLease{service: s, application: application, applicationID: appID, notificationID: notificationID, generation: row.LeaseGeneration, origin: runtime.OriginEpoch, source: runtime.SourceEpoch}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return provisional, nil
}

// Renew cannot revive an expired, released or replaced lease. Holds and lineage
// changes fail closed; a caller must cancel local work on renewal failure.
func (s *Execution) Renew(ctx context.Context, lease *ExecutionLease) error {
	return s.changeLease(ctx, lease, true)
}

// Release clears only this still-live owner/generation and preserves generation.
// A held deployment may leave its lease to expire instead of changing state.
func (s *Execution) Release(ctx context.Context, lease *ExecutionLease) error {
	return s.changeLease(ctx, lease, false)
}
func (s *Execution) changeLease(ctx context.Context, lease *ExecutionLease, renew bool) error {
	if !s.owns(lease) {
		return ErrExecutionInput
	}
	return s.transaction(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		if err := s.lockLeaseBase(ctx, q, lease); err != nil {
			return err
		}
		runtime := sqlc.LockNotificationRuntimeRow{OriginEpoch: lease.origin, SourceEpoch: lease.source}
		if err := lockExecutionCampaign(ctx, q, lease.applicationID, lease.notificationID, runtime); err != nil {
			return err
		}
		var count int64
		var err error
		if renew {
			count, err = q.RenewExecutionLease(ctx, sqlc.RenewExecutionLeaseParams{ApplicationID: lease.applicationID, NotificationID: lease.notificationID,
				Owner: s.owner, Generation: lease.generation, OriginEpoch: lease.origin, SourceEpoch: lease.source, LeaseSeconds: int32(s.policy.LeaseDuration / time.Second)})
		} else {
			count, err = q.ReleaseExecutionLease(ctx, sqlc.ReleaseExecutionLeaseParams{ApplicationID: lease.applicationID, NotificationID: lease.notificationID,
				Owner: s.owner, Generation: lease.generation, OriginEpoch: lease.origin, SourceEpoch: lease.source})
		}
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrExecutionUnavailable
		}
		return nil
	})
}

// Reserve charges one due eligible PENDING intent under a live lease. The caller
// must first obtain concurrency/rate/breaker permits and approve the provider
// route. Unresolved charges cannot repeat in the same generation. A takeover
// retains their count/retry floor. No network I/O or blind transaction retry occurs.
func (s *Execution) Reserve(ctx context.Context, lease *ExecutionLease, deliveryID pgtype.UUID) (*AttemptReservation, error) {
	if !s.owns(lease) || !executionUUID(deliveryID) {
		return nil, ErrExecutionInput
	}
	var provisional *AttemptReservation
	err := s.transaction(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		if err := s.lockLeaseBase(ctx, q, lease); err != nil {
			return err
		}
		identity, err := q.GetExecutionIntentIdentity(ctx, sqlc.GetExecutionIntentIdentityParams{ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: deliveryID})
		if err != nil {
			return executionMissing(err)
		}
		// Identity discovery takes no child lock. The immutable user is locked before
		// the campaign; installation/binding locks follow it in the shared protocol.
		if _, err = q.LockInstallationUser(ctx, sqlc.LockInstallationUserParams{ApplicationID: lease.applicationID, UserID: identity.OwnerUserID}); err != nil {
			return executionMissing(err)
		}
		runtime := sqlc.LockNotificationRuntimeRow{OriginEpoch: lease.origin, SourceEpoch: lease.source}
		if err := lockExecutionCampaign(ctx, q, lease.applicationID, lease.notificationID, runtime); err != nil {
			return err
		}
		installation, err := q.LockExecutionInstallation(ctx, sqlc.LockExecutionInstallationParams{ApplicationID: lease.applicationID, InstallationID: identity.InstallationID})
		if err != nil {
			return executionMissing(err)
		}
		binding, err := q.LockExecutionBinding(ctx, sqlc.LockExecutionBindingParams{ApplicationID: lease.applicationID, BindingID: identity.BindingID})
		if err != nil {
			return executionMissing(err)
		}
		if !installation.OwnerUserID.Valid || installation.OwnerUserID.Int64 != identity.OwnerUserID || installation.OwnerEpoch != identity.OwnerEpoch ||
			binding.RetiredAt.Valid || binding.InstallationID != identity.InstallationID || binding.OwnerUserID != identity.OwnerUserID || binding.OwnerEpoch != identity.OwnerEpoch {
			return ErrExecutionUnavailable
		}
		material, err := s.freezeReceipt(ctx, q, lease, deliveryID, identity, installation.PublicID)
		if err != nil {
			return err
		}
		attemptID, err := newReplayClaimToken()
		if err != nil {
			return err
		}
		_, err = q.ReserveExecutionAttempt(ctx, sqlc.ReserveExecutionAttemptParams{ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: deliveryID,
			AttemptID: attemptID, Owner: s.owner, Generation: lease.generation, OriginEpoch: lease.origin, SourceEpoch: lease.source,
			MaxAttempts: s.policy.MaxAttempts, RetryFloorSeconds: int32(s.policy.RetryFloor / time.Second)})
		if err != nil {
			return executionMissing(err)
		}
		provisional = &AttemptReservation{lease: lease, deliveryID: deliveryID, attemptID: attemptID, bindingID: identity.BindingID, installationID: identity.InstallationID, ownerUserID: identity.OwnerUserID, ownerEpoch: identity.OwnerEpoch, material: material}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return provisional, nil
}

func (s *Execution) owns(lease *ExecutionLease) bool {
	return s != nil && s.db != nil && executionUUID(s.owner) && lease != nil && lease.service == s && lease.generation > 0
}
func executionUUID(id pgtype.UUID) bool { return id.Valid && id.Bytes != [16]byte{} }
func executionMissing(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrExecutionUnavailable
	}
	return err
}
func (s *Execution) lockBase(ctx context.Context, q *sqlc.Queries, application pgtype.UUID) (int64, sqlc.LockNotificationRuntimeRow, error) {
	runtime, err := q.LockNotificationRuntime(ctx)
	if err != nil {
		return 0, runtime, err
	}
	if runtime.Sealed || runtime.MigrationHold {
		return 0, runtime, ErrExecutionHeld
	}
	app, err := q.LockExecutionApplication(ctx, application)
	if err != nil {
		return 0, runtime, executionMissing(err)
	}
	policy, err := q.GetApplicationDispatchPolicy(ctx, app.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, runtime, ErrExecutionHeld
	}
	if err != nil {
		return 0, runtime, err
	}
	if app.Status != "ACTIVE" || policy.HoldNew || !policy.CoverageApproved {
		return 0, runtime, ErrExecutionHeld
	}
	return app.ID, runtime, nil
}
func (s *Execution) lockLeaseBase(ctx context.Context, q *sqlc.Queries, lease *ExecutionLease) error {
	appID, runtime, err := s.lockBase(ctx, q, lease.application)
	if err != nil {
		return err
	}
	if appID != lease.applicationID || runtime.OriginEpoch != lease.origin || runtime.SourceEpoch != lease.source {
		return ErrExecutionHeld
	}
	return nil
}
func lockExecutionCampaign(ctx context.Context, q *sqlc.Queries, appID, notificationID int64, runtime sqlc.LockNotificationRuntimeRow) error {
	campaign, err := q.LockPreparationCampaign(ctx, sqlc.LockPreparationCampaignParams{ApplicationID: appID, NotificationID: notificationID})
	if err != nil {
		return executionMissing(err)
	}
	if campaign.Status != "PROCESSING" || !campaign.DeadlineAt.Valid || campaign.OriginEpoch != runtime.OriginEpoch || campaign.SourceEpoch != runtime.SourceEpoch {
		return ErrExecutionHeld
	}
	complete, err := q.GetCompletePreparation(ctx, sqlc.GetCompletePreparationParams{ApplicationID: appID, NotificationID: notificationID})
	if err != nil {
		return executionMissing(err)
	}
	if complete.OriginEpoch != runtime.OriginEpoch || complete.SourceEpoch != runtime.SourceEpoch {
		return ErrExecutionHeld
	}
	return nil
}
func (s *Execution) transaction(ctx context.Context, run func(context.Context, *sqlc.Queries) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	if err := run(ctx, sqlc.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReceiptMaterial returns committed signing inputs only. It cannot recreate or
// transfer provider execution authority. Nil/unacknowledged reservations return false.
func (r *AttemptReservation) ReceiptMaterial() (receipt.Material, bool) {
	if r == nil || !r.material.Valid() {
		return receipt.Material{}, false
	}
	return r.material, true
}
func (s *Execution) freezeReceipt(ctx context.Context, q *sqlc.Queries, lease *ExecutionLease, deliveryID pgtype.UUID, identity sqlc.GetExecutionIntentIdentityRow, installationPublicID pgtype.UUID) (receipt.Material, error) {
	none := receipt.Material{}
	delivery, err := q.LockReceiptDelivery(ctx, sqlc.LockReceiptDeliveryParams{ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: deliveryID})
	if err != nil {
		return none, executionMissing(err)
	}
	if delivery.Status != "PENDING" {
		return none, ErrExecutionUnavailable
	}
	if err := q.FreezeReceiptMetadata(ctx, sqlc.FreezeReceiptMetadataParams{ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: deliveryID,
		KeyID: s.policy.Receipt.KeyID, Environment: s.policy.Receipt.Environment, WindowSeconds: int64(s.policy.Receipt.ObservationWindow / time.Second)}); err != nil {
		return none, err
	}
	frozen, err := q.GetFrozenReceiptMetadata(ctx, sqlc.GetFrozenReceiptMetadataParams{ApplicationID: lease.applicationID, DeliveryID: deliveryID})
	if errors.Is(err, pgx.ErrNoRows) {
		return none, ErrExecutionHeld
	}
	if err != nil {
		return none, err
	}
	material := receipt.Material{Version: frozen.Version, KeyID: frozen.KeyID, Environment: frozen.Environment,
		Scope: receipt.Scope{ApplicationID: lease.application, OriginEpoch: lease.origin, NotificationID: lease.notificationID, DeliveryID: deliveryID, BindingID: identity.BindingID, InstallationID: installationPublicID, OwnerEpoch: identity.OwnerEpoch, ExpiresUnix: frozen.ExpiresUnix}}
	if !frozen.CoversDeadline || !material.Valid() || material.Environment != s.policy.Receipt.Environment {
		return none, ErrExecutionHeld
	}
	return material, nil
}
