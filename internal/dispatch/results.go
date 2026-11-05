package dispatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
	"hello/internal/fcm"
	"hello/internal/kafka"
)

// RecordedResult contains acknowledged persistence evidence only. An error
// returns its zero value even if a lost acknowledgement hid an actual commit.
type RecordedResult struct {
	AlreadyRecorded    bool
	ProjectionChanged  bool
	BindingInvalidated bool
	DispatchHeld       bool
	DeliveryVersion    int64
}

// RecordResult fences ledger, conditional invalidation, holds and update outbox
// in one transaction. Retry this same evidence on DB uncertainty; never resend
// the provider call. Current ownership/app holds do not discard in-flight results.
func (s *Execution) RecordResult(ctx context.Context, result *SubmissionResult) (RecordedResult, error) {
	var recorded RecordedResult
	if result == nil || result.reservation == nil || !s.owns(result.reservation.lease) || result.reservation.use == nil || !result.reservation.use.Load() || !validResult(result.outcome) {
		return recorded, ErrExecutionInput
	}
	r, out := result.reservation, result.outcome
	encoded, err := json.Marshal(out)
	if err != nil {
		return recorded, ErrExecutionInput
	}
	fingerprint := sha256.Sum256(encoded)
	err = s.transaction(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		lease := r.lease
		if err := s.lockResultBase(ctx, q, lease, configurationFailure(out)); err != nil {
			return err
		}
		if err := lockExecutionCampaign(ctx, q, lease.applicationID, lease.notificationID, sqlc.LockNotificationRuntimeRow{OriginEpoch: lease.origin, SourceEpoch: lease.source}); err != nil {
			return err
		}
		installation, err := q.LockResultInstallation(ctx, sqlc.LockResultInstallationParams{ApplicationID: lease.applicationID, InstallationID: r.installationID})
		if err != nil {
			return executionMissing(err)
		}
		binding, err := q.LockResultBinding(ctx, sqlc.LockResultBindingParams{ApplicationID: lease.applicationID, BindingID: r.bindingID})
		if err != nil {
			return executionMissing(err)
		}
		row, err := q.LockResultDelivery(ctx, sqlc.LockResultDeliveryParams{ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: r.deliveryID, AttemptID: r.attemptID, Generation: lease.generation, OriginEpoch: lease.origin, SourceEpoch: lease.source, Owner: s.owner})
		if err != nil {
			return executionMissing(err)
		}
		if row.BindingID != r.bindingID || row.InstallationID != r.installationID || row.OwnerUserID != r.ownerUserID || row.OwnerEpoch != r.ownerEpoch {
			return ErrExecutionHeld
		}
		if row.AttemptResolvedAt.Valid {
			if !bytes.Equal(row.LastResultFingerprint, fingerprint[:]) {
				return ErrExecutionHeld
			}
			recorded = RecordedResult{AlreadyRecorded: true, DeliveryVersion: row.DeliveryVersion}
			return nil
		}
		desired, changed := resultMutation(row, out, s.policy.MaxAttempts)
		if changed && row.DeliveryVersion == math.MaxInt64 {
			return ErrExecutionHeld
		}
		increment := int64(0)
		if changed {
			increment = 1
		}
		updated, err := q.WriteDeliveryResult(ctx, sqlc.WriteDeliveryResultParams{
			ApplicationID: lease.applicationID, DeliveryID: r.deliveryID, AttemptID: r.attemptID, Generation: lease.generation,
			OriginEpoch: lease.origin, SourceEpoch: lease.source, Owner: s.owner, Status: desired.Status,
			ProviderMessageID: desired.ProviderMessageID, ProviderAcceptedAt: desired.ProviderAcceptedAt, FailedAt: desired.FailedAt, TerminalReason: desired.TerminalReason,
			Disposition: resultText(string(out.Disposition)), Reason: resultText(string(out.Reason)), Fingerprint: fingerprint[:], RecordedAt: row.RecordedAt, NextAttemptAt: desired.NextAttemptAt, VersionIncrement: increment,
		})
		if err != nil {
			return executionMissing(err)
		}
		if out.Disposition == fcm.Rejected && out.Reason == fcm.Unregistered && !binding.RetiredAt.Valid && binding.OwnerEpoch == r.ownerEpoch && binding.OwnerUserID == r.ownerUserID &&
			installation.OwnerEpoch == r.ownerEpoch && installation.OwnerUserID.Valid && installation.OwnerUserID.Int64 == r.ownerUserID {
			if installation.Version == math.MaxInt64 {
				return ErrExecutionHeld
			}
			n, err := q.RetireInstallationBinding(ctx, sqlc.RetireInstallationBindingParams{ApplicationID: lease.applicationID, ID: r.bindingID})
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrExecutionUnavailable
			}
			_, err = q.AdvanceMobileInstallation(ctx, sqlc.AdvanceMobileInstallationParams{ApplicationID: lease.applicationID, ID: r.installationID, OwnerUserID: installation.OwnerUserID, OwnerEpoch: installation.OwnerEpoch, ExpectedVersion: installation.Version})
			if err != nil {
				return executionMissing(err)
			}
			recorded.BindingInvalidated = true
		}
		if configurationFailure(out) {
			if err := q.HoldResultDispatch(ctx, lease.applicationID); err != nil {
				return err
			}
			recorded.DispatchHeld = true
		}
		if changed {
			id, err := newReplayClaimToken()
			if err != nil {
				return err
			}
			event := deliveryProjection(lease, updated, id, row.RecordedAt.Time)
			if event.Validate() != nil {
				return ErrExecutionHeld
			}
			payload, err := json.Marshal(event)
			if err != nil {
				return err
			}
			if err = q.InsertDeliveryUpdate(ctx, sqlc.InsertDeliveryUpdateParams{EventID: id, ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: r.deliveryID, DeliveryVersion: updated.DeliveryVersion, OriginEpoch: lease.origin, SourceEpoch: lease.source, Payload: payload, OccurredAt: row.RecordedAt}); err != nil {
				return err
			}
		}
		recorded.ProjectionChanged = changed
		recorded.DeliveryVersion = updated.DeliveryVersion
		return nil
	})
	if err != nil {
		return RecordedResult{}, err
	}
	return recorded, nil
}

func (s *Execution) lockResultBase(ctx context.Context, q *sqlc.Queries, lease *ExecutionLease, hold bool) error {
	runtime, err := q.LockNotificationRuntime(ctx)
	if err != nil {
		return err
	}
	if runtime.Sealed || runtime.MigrationHold || runtime.OriginEpoch != lease.origin || runtime.SourceEpoch != lease.source {
		return ErrExecutionHeld
	}
	app, err := q.LockExecutionApplication(ctx, lease.application)
	if err != nil {
		return executionMissing(err)
	}
	if app.ID != lease.applicationID {
		return ErrExecutionHeld
	}
	if hold {
		_, err = q.LockResultPolicy(ctx, app.ID)
	} else {
		_, err = q.GetApplicationDispatchPolicy(ctx, app.ID)
	}
	return executionMissing(err)
}
func configurationFailure(out fcm.Outcome) bool {
	return out.Reason == fcm.CredentialFailure || out.Reason == fcm.APNsCredentials || out.Reason == fcm.SenderMismatch
}
func validResult(out fcm.Outcome) bool {
	if out.PhysicalRequests < 0 || out.PhysicalRequests > 1 {
		return false
	}
	switch out.Disposition {
	case fcm.Accepted:
		return out.Reason == fcm.Success && out.PhysicalRequests == 1 && len(out.ProviderMessageID) > 0
	case fcm.Rejected:
		if out.PhysicalRequests != 1 || out.ProviderMessageID != "" {
			return false
		}
		switch out.Reason {
		case fcm.Unregistered, fcm.InvalidPayload, fcm.SenderMismatch, fcm.CredentialFailure, fcm.APNsCredentials, fcm.Quota, fcm.Unavailable, fcm.Internal:
			return true
		}
	case fcm.Unconfirmed:
		if out.PhysicalRequests != 1 || out.ProviderMessageID != "" {
			return false
		}
		switch out.Reason {
		case fcm.Unknown, fcm.Network, fcm.InvalidResponse:
			return true
		}
	case fcm.NotSubmitted:
		if out.PhysicalRequests != 0 || out.ProviderMessageID != "" {
			return false
		}
		switch out.Reason {
		case fcm.Network, fcm.InvalidPayload, fcm.CredentialFailure, fcm.Closed, fcm.PermitUsed, fcm.Cancelled, fcm.AuthorizationFailure:
			return true
		}
	}
	return false
}

func resultMutation(row sqlc.LockResultDeliveryRow, out fcm.Outcome, maxAttempts int32) (sqlc.LockResultDeliveryRow, bool) {
	desired := row
	if out.Disposition == fcm.Accepted {
		if !desired.ProviderMessageID.Valid {
			desired.ProviderMessageID = resultText(out.ProviderMessageID)
			desired.ProviderAcceptedAt = row.RecordedAt
		}
		if desired.Status == "PENDING" {
			desired.Status = "SENT"
		}
	} else if desired.Status == "PENDING" {
		permanent := out.Disposition == fcm.Rejected && (out.Reason == fcm.Unregistered || out.Reason == fcm.InvalidPayload || configurationFailure(out)) || out.Disposition == fcm.NotSubmitted && (configurationFailure(out) || out.Reason == fcm.InvalidPayload)
		reason := ""
		if permanent {
			reason = string(out.Reason)
		} else {
			floor := 10 * time.Second
			if out.Reason == fcm.Quota {
				floor = time.Minute
			}
			next := row.RecordedAt.Time.Add(floor)
			if row.NextAttemptAt.Time.After(next) {
				next = row.NextAttemptAt.Time
			}
			if out.RetryAt.After(next) {
				next = out.RetryAt
			}
			rounded := next.UTC().Truncate(time.Microsecond)
			if rounded.Before(next) {
				rounded = rounded.Add(time.Microsecond)
			}
			next = rounded
			desired.NextAttemptAt = resultTime(next)
			if !row.DeadlineAt.Time.After(row.RecordedAt.Time) || !next.Before(row.DeadlineAt.Time) {
				reason = "ttl_expired"
			} else if row.AttemptCount >= maxAttempts {
				reason = "retry_budget_exhausted"
			}
		}
		if reason != "" {
			desired.Status = "FAILED"
			desired.TerminalReason = resultText(reason)
			desired.FailedAt = row.RecordedAt
		}
	}
	changed := desired.Status != row.Status || desired.ProviderMessageID != row.ProviderMessageID || desired.ProviderAcceptedAt != row.ProviderAcceptedAt || desired.FailedAt != row.FailedAt || desired.TerminalReason != row.TerminalReason
	return desired, changed
}
func resultText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }
func resultTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC().Truncate(time.Microsecond), Valid: true}
}
func eventTimePointer(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC().Truncate(time.Microsecond)
	return &v
}
func eventTextPointer(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	v := t.String
	return &v
}
func deliveryProjection(lease *ExecutionLease, row sqlc.NotificationDeliveryIntent, id pgtype.UUID, at time.Time) kafka.ScopedDeliveryUpdated {
	opened := row.ObservedOpenedAt.Valid || row.Status == "OPENED"
	delivered := opened || row.ObservedDeliveredAt.Valid || row.Status == "DELIVERED"
	return kafka.ScopedDeliveryUpdated{SchemaVersion: 2, ApplicationID: lease.application.String(), OriginEpoch: lease.origin.String(), SourceEpoch: lease.source.String(), EventID: id.String(), DeliveryID: row.ID.String(), DeliveryVersion: row.DeliveryVersion, EventKind: "submission", NotificationID: lease.notificationID, UserID: row.OwnerUserID, Provider: "FCM", Status: row.Status,
		ProviderMessageID: eventTextPointer(row.ProviderMessageID), ProviderAcceptedAt: eventTimePointer(row.ProviderAcceptedAt), ObservedDelivered: delivered, ObservedDeliveredAt: eventTimePointer(row.ObservedDeliveredAt), ObservedOpened: opened, ObservedOpenedAt: eventTimePointer(row.ObservedOpenedAt), FailedAt: eventTimePointer(row.FailedAt), Reason: eventTextPointer(row.TerminalReason), OccurredAt: at.UTC().Truncate(time.Microsecond)}
}
