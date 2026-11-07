package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
	"hello/internal/fcm"
	"hello/internal/installation"
	"hello/internal/pushpayload"
	"hello/internal/receipt"
)

// Submitter binds acknowledged DB authority to one trusted transport, signer and
// address protector. It owns no goroutines or resources; its owner closes FCM.
// It returns evidence only; fenced persistence/recovery remains a separate step.
type Submitter struct {
	execution *Execution
	client    *fcm.Client
	signer    *receipt.Signer
	protector *installation.Protector
}

// NewSubmitter requires explicit, independently constructed trusted dependencies.
// This does not verify production IAM, quota allocation or deployment activation.
func NewSubmitter(execution *Execution, client *fcm.Client, signer *receipt.Signer, protector *installation.Protector) (*Submitter, error) {
	if execution == nil || execution.db == nil || client == nil || signer == nil || protector == nil {
		return nil, ErrExecutionInput
	}
	return &Submitter{execution: execution, client: client, signer: signer, protector: protector}, nil
}

// SubmissionResult retains the original charge and safe transport evidence for
// later fenced persistence. It grants no permission for another provider call.
type SubmissionResult struct {
	reservation *AttemptReservation
	outcome     fcm.Outcome
}

// Outcome returns a safe value copy; a nil result carries no evidence.
func (r *SubmissionResult) Outcome() (fcm.Outcome, bool) {
	if r == nil || r.reservation == nil {
		return fcm.Outcome{}, false
	}
	return r.outcome, true
}

// Send verifies an advisory route and obtains a permit before charging. It never
// retries a transaction containing provider I/O or returns an unacknowledged charge.
func (s *Submitter) Send(ctx context.Context, lease *ExecutionLease, delivery pgtype.UUID) (*SubmissionResult, error) {
	if s == nil || !s.execution.owns(lease) || !executionUUID(delivery) {
		return nil, ErrExecutionInput
	}
	var route sqlc.GetExecutionProviderRouteRow
	err := s.execution.transaction(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		var err error
		route, err = q.GetExecutionProviderRoute(ctx, sqlc.GetExecutionProviderRouteParams{ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: delivery})
		return executionMissing(err)
	})
	if err != nil {
		return nil, err
	}
	allowed := pushpayload.Route{ApplicationID: lease.application, Environment: s.execution.policy.Receipt.Environment, Namespace: route.ProviderNamespace, Key: route.ProviderRouteKey, Platform: route.Platform, AddressType: route.AddressType}
	if !s.client.Allows(allowed) {
		return nil, ErrExecutionHeld
	}
	permit, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer permit.Release()
	reservation, err := s.execution.Reserve(ctx, lease, delivery)
	if err != nil {
		return nil, err
	}
	return s.SubmitReserved(ctx, reservation, permit)
}

// SubmitReserved consumes the reservation across all copies before preparation.
// The caller must have acquired this client's permit before Reserve. Every path
// releases that permit. A failed check/sign/decrypt never restores the charge.
func (s *Submitter) SubmitReserved(ctx context.Context, reservation *AttemptReservation, permit *fcm.Permit) (*SubmissionResult, error) {
	if permit != nil {
		defer permit.Release()
	}
	if s == nil || reservation == nil || !s.execution.owns(reservation.lease) || reservation.use == nil || permit == nil {
		return nil, ErrExecutionInput
	}
	if !reservation.use.CompareAndSwap(false, true) {
		return nil, ErrExecutionUnavailable
	}
	prepared, err := s.execution.loadSubmission(ctx, reservation)
	if err != nil {
		return nil, err
	}
	plain, err := s.protector.Open(installation.BindingIdentity{ApplicationID: reservation.lease.applicationID, InstallationID: reservation.installationID, BindingID: reservation.bindingID}, prepared.binding.EncryptionKeyVersion, prepared.binding.AddressCiphertext)
	if err != nil {
		return nil, ErrExecutionHeld
	}
	defer clear(plain)
	capability, err := s.signer.SignMaterial(reservation.material)
	if err != nil {
		return nil, ErrExecutionHeld
	}
	data, err := submissionData(prepared.campaign.Payload)
	if err != nil {
		return nil, err
	}
	request := pushpayload.Request{
		Scope: reservation.material.Scope, Capability: capability, Deadline: prepared.campaign.DeadlineAt.Time,
		Title: prepared.campaign.Title, Body: prepared.campaign.Body, Data: data,
		Destination: pushpayload.Destination{ApplicationID: reservation.lease.application, BindingID: reservation.bindingID, InstallationID: prepared.installation.PublicID, OwnerEpoch: reservation.ownerEpoch,
			Namespace: prepared.binding.ProviderNamespace, RouteKey: prepared.binding.ProviderRouteKey, Platform: prepared.binding.Platform, AddressType: prepared.binding.AddressType, Address: string(plain)},
	}
	var authorizationErr error
	outcome := s.client.SendAuthorized(ctx, permit, request, func(ctx context.Context) (time.Time, error) {
		current, err := s.execution.loadSubmission(ctx, reservation)
		if err == nil && !prepared.same(current) {
			err = ErrExecutionHeld
		}
		authorizationErr = err
		if err != nil {
			return time.Time{}, err
		}
		return current.until, nil
	})
	result := &SubmissionResult{reservation: reservation, outcome: outcome}
	return result, authorizationErr
}

type submissionSnapshot struct {
	campaign     sqlc.LockExecutionSubmissionRow
	binding      sqlc.InstallationBinding
	installation sqlc.LockExecutionInstallationRow
	until        time.Time
}

func (s *Execution) loadSubmission(parent context.Context, r *AttemptReservation) (submissionSnapshot, error) {
	var snapshot submissionSnapshot
	started := time.Now()
	lease := r.lease
	err := s.transaction(parent, func(ctx context.Context, q *sqlc.Queries) error {
		if err := s.lockLeaseBase(ctx, q, lease); err != nil {
			return err
		}
		identity, err := q.GetExecutionIntentIdentity(ctx, sqlc.GetExecutionIntentIdentityParams{ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: r.deliveryID})
		if err != nil {
			return executionMissing(err)
		}
		if identity.AuthorizationRevision == 0 || identity.AuthorizationRevision != r.routeRevision || identity.BindingID != r.bindingID || identity.InstallationID != r.installationID || identity.OwnerUserID != r.ownerUserID || identity.OwnerEpoch != r.ownerEpoch {
			return ErrExecutionHeld
		}
		if _, err = q.LockInstallationUser(ctx, sqlc.LockInstallationUserParams{ApplicationID: lease.applicationID, UserID: r.ownerUserID}); err != nil {
			return executionMissing(err)
		}
		if err = lockDeviceExecutionCampaign(ctx, q, lease); err != nil {
			return err
		}
		snapshot.installation, err = q.LockExecutionInstallation(ctx, sqlc.LockExecutionInstallationParams{ApplicationID: lease.applicationID, InstallationID: r.installationID})
		if err != nil {
			return executionMissing(err)
		}
		snapshot.binding, err = q.LockExecutionBinding(ctx, sqlc.LockExecutionBindingParams{ApplicationID: lease.applicationID, BindingID: r.bindingID})
		if err != nil {
			return executionMissing(err)
		}
		i, b := snapshot.installation, snapshot.binding
		if !i.OwnerUserID.Valid || i.OwnerUserID.Int64 != r.ownerUserID || i.OwnerEpoch != r.ownerEpoch || i.PublicID != r.material.Scope.InstallationID ||
			b.RetiredAt.Valid || b.InstallationID != r.installationID || b.OwnerUserID != r.ownerUserID || b.OwnerEpoch != r.ownerEpoch {
			return ErrExecutionUnavailable
		}
		snapshot.campaign, err = q.LockExecutionSubmission(ctx, sqlc.LockExecutionSubmissionParams{ApplicationID: lease.applicationID, NotificationID: lease.notificationID, DeliveryID: r.deliveryID, AttemptID: r.attemptID,
			Owner: s.owner, Generation: lease.generation, OriginEpoch: lease.origin, SourceEpoch: lease.source, MaxAttempts: s.policy.MaxAttempts})
		if err != nil {
			return executionMissing(err)
		}
		frozen, err := q.GetFrozenReceiptMetadata(ctx, sqlc.GetFrozenReceiptMetadataParams{ApplicationID: lease.applicationID, DeliveryID: r.deliveryID})
		if err != nil {
			return executionMissing(err)
		}
		if !frozen.CoversDeadline || frozen.Version != r.material.Version || frozen.KeyID != r.material.KeyID || frozen.Environment != r.material.Environment || frozen.ExpiresUnix != r.material.Scope.ExpiresUnix {
			return ErrExecutionHeld
		}
		// Start before the transaction: DB/commit latency reduces this local window.
		micros := snapshot.campaign.ValidMicros
		maxMicros := int64(s.policy.LeaseDuration / time.Microsecond)
		if micros <= 0 {
			return ErrExecutionUnavailable
		}
		snapshot.until = started.Add(time.Duration(min(micros, maxMicros)) * time.Microsecond)
		snapshot.binding.AddressCiphertext = bytes.Clone(b.AddressCiphertext)
		snapshot.campaign.Payload = bytes.Clone(snapshot.campaign.Payload)
		return nil
	})
	if err != nil {
		return submissionSnapshot{}, err
	}
	if !snapshot.until.After(time.Now()) {
		return submissionSnapshot{}, ErrExecutionUnavailable
	}
	return snapshot, nil
}

func (s submissionSnapshot) same(other submissionSnapshot) bool {
	a, b := s.binding, other.binding
	return s.campaign.Title == other.campaign.Title && s.campaign.Body == other.campaign.Body && bytes.Equal(s.campaign.Payload, other.campaign.Payload) && s.campaign.DeadlineAt.Valid == other.campaign.DeadlineAt.Valid && s.campaign.DeadlineAt.Time.Equal(other.campaign.DeadlineAt.Time) &&
		a.ProviderNamespace == b.ProviderNamespace && a.ProviderRouteKey == b.ProviderRouteKey && a.Platform == b.Platform && a.AddressType == b.AddressType && a.EncryptionKeyVersion == b.EncryptionKeyVersion && bytes.Equal(a.AddressCiphertext, b.AddressCiphertext) && s.installation.PublicID == other.installation.PublicID
}

func (r AttemptReservation) String() string               { return "[REDACTED]" }
func (r AttemptReservation) GoString() string             { return r.String() }
func (r AttemptReservation) LogValue() slog.Value         { return slog.StringValue(r.String()) }
func (r AttemptReservation) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (r ExecutionLease) String() string                   { return "[REDACTED]" }
func (r ExecutionLease) GoString() string                 { return r.String() }
func (r ExecutionLease) LogValue() slog.Value             { return slog.StringValue(r.String()) }
func (r ExecutionLease) MarshalJSON() ([]byte, error)     { return []byte(`"[REDACTED]"`), nil }

func submissionData(raw []byte) (map[string]string, error) {
	var fields map[string]json.RawMessage
	if len(raw) > 65536 || json.Unmarshal(raw, &fields) != nil || fields == nil || len(fields) > 100 {
		return nil, ErrExecutionHeld
	}
	data := make(map[string]string, len(fields))
	for key, rawValue := range fields {
		var value *string
		if json.Unmarshal(rawValue, &value) != nil || value == nil {
			return nil, ErrExecutionHeld
		}
		data[key] = *value
	}
	return data, nil
}
