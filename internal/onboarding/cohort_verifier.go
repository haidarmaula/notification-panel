package onboarding

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/fcm"
	"hello/internal/installation"
)

// CohortVerificationStore is the protected persistence boundary, not an API
// repository. Claim must atomically lease one approved cohort member and resolve
// its current configuration and platform binding. It must enforce ACTIVE/HELD,
// approval scope and expiry, cohort membership, consent and test designation.
// A nil claim with no error means there is nothing to check.
// Finish must conditionally persist evidence only if the lease and all claimed
// lineage are still current (including approval, configuration and binding epoch).
// Neither operation may grant dispatch permission or modify the installation.
type CohortVerificationStore interface {
	ClaimCohortTarget(ctx context.Context, applicationID pgtype.UUID, environment Environment, leaseOwner pgtype.UUID) (*CohortTargetClaim, error)
	FinishCohortTarget(ctx context.Context, fence CohortTargetFence, evidence CohortTargetEvidence) (bool, error)
}

// CohortTargetFence identifies the exact leased member check without its address.
type CohortTargetFence struct {
	ApplicationID, OperationID, CohortID, ApprovalID, ConfigurationID pgtype.UUID
	InstallationID, LeaseOwner                                        pgtype.UUID
	Environment                                                       Environment
	BindingEpoch, LeaseToken                                          int64
}

// CohortTargetClaim is resolved only by the protected store from current state.
type CohortTargetClaim struct {
	Fence                                              CohortTargetFence
	FileReference                                      pgtype.UUID
	Project, ServiceAccountEmail, AddressType, Address string
}

// CohortTargetEvidence contains only sanitized dry-run results. PASSED does not
// imply device receipt or authorize delivery.
type CohortTargetEvidence struct {
	Outcome, FailureCode string
}

// CohortVerifier checks one leased target per run using FCM validate_only.
type CohortVerifier struct {
	store    CohortVerificationStore
	policy   VerifierPolicy
	owner    pgtype.UUID
	validate func(context.Context, fcm.FileCredentialPolicy, string, string, string) error
}

// NewCohortVerifier scopes target checks to the protected startup policy.
func NewCohortVerifier(store CohortVerificationStore, policy VerifierPolicy) (*CohortVerifier, error) {
	if store == nil || !nonzeroUUID(policy.ApplicationID) ||
		(policy.Environment != EnvironmentStaging && policy.Environment != EnvironmentProduction) ||
		!filepath.IsAbs(policy.CredentialRoot) || filepath.Clean(policy.CredentialRoot) != policy.CredentialRoot ||
		policy.CredentialRoot == string(filepath.Separator) {
		return nil, ErrInvalidVerifier
	}
	owner, err := uuid.NewRandom()
	if err != nil {
		return nil, ErrInvalidVerifier
	}
	// Do not retain database connection details in the executor.
	return &CohortVerifier{
		store: store, owner: pgtype.UUID{Bytes: owner, Valid: true}, validate: fcm.ValidateFileTarget,
		policy: VerifierPolicy{ApplicationID: policy.ApplicationID, Environment: policy.Environment, CredentialRoot: policy.CredentialRoot},
	}, nil
}

// RunOnce claims and checks at most one target. A stale or failed evidence write
// is an error even if the provider accepted the dry run.
func (v *CohortVerifier) RunOnce(ctx context.Context) (bool, error) {
	if v == nil || v.store == nil || v.validate == nil || ctx == nil {
		return false, ErrInvalidVerifier
	}
	checkCtx, cancel := context.WithTimeout(ctx, verificationTimeout)
	defer cancel()
	if checkCtx.Err() != nil {
		return false, ErrVerifierUnavailable
	}
	claim, err := v.store.ClaimCohortTarget(checkCtx, v.policy.ApplicationID, v.policy.Environment, v.owner)
	if err != nil {
		return false, ErrVerifierUnavailable
	}
	if claim == nil {
		return false, nil
	}
	fence := claim.Fence
	if fence.ApplicationID != v.policy.ApplicationID || fence.Environment != v.policy.Environment ||
		fence.LeaseOwner != v.owner || fence.LeaseToken <= 0 || fence.BindingEpoch <= 0 ||
		!nonzeroUUID(fence.OperationID) || !nonzeroUUID(fence.CohortID) || !nonzeroUUID(fence.ApprovalID) ||
		!nonzeroUUID(fence.ConfigurationID) || !nonzeroUUID(fence.InstallationID) ||
		claim.FileReference != fence.ConfigurationID || checkCtx.Err() != nil {
		return true, ErrVerifierUnavailable
	}
	policy := fcm.FileCredentialPolicy{
		Path:                filepath.Join(v.policy.CredentialRoot, fence.ConfigurationID.String(), "service-account.json"),
		CredentialProjectID: claim.Project, ServiceAccountEmail: claim.ServiceAccountEmail,
	}
	if (claim.AddressType != installation.FCMFID && claim.AddressType != installation.FCMRegistrationToken) ||
		!installation.ValidAddress(claim.Address) || policy.ValidateIdentity(claim.Project) != nil {
		return true, ErrVerifierUnavailable
	}
	checkErr := v.validate(checkCtx, policy, claim.Project, claim.AddressType, claim.Address)
	if checkCtx.Err() != nil {
		checkErr = fcm.ErrVerificationTemporary
	}
	outcome, code := cohortTargetOutcome(checkErr)
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	applied, err := v.store.FinishCohortTarget(finishCtx, fence, CohortTargetEvidence{Outcome: outcome, FailureCode: code})
	if err != nil || !applied {
		return true, ErrVerifierUnavailable
	}
	return true, nil
}

func nonzeroUUID(id pgtype.UUID) bool {
	return id.Valid && id.Bytes != [16]byte{}
}

func cohortTargetOutcome(err error) (string, string) {
	switch {
	case errors.Is(err, fcm.ErrTargetUnregistered):
		return "FAILED", "FCM_TARGET_UNREGISTERED"
	case errors.Is(err, fcm.ErrTargetSenderMismatch):
		return "FAILED", "FCM_TARGET_SENDER_MISMATCH"
	case errors.Is(err, fcm.ErrTargetCheckRejected):
		return "FAILED", "FCM_TARGET_CHECK_REJECTED"
	default:
		return verificationOutcome(err)
	}
}
