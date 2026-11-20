package onboarding

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"hello/internal/database/sqlc"
	"hello/internal/installation"
)

// PostgresCohortStore resolves encrypted bindings using a protected database connection.
type PostgresCohortStore struct {
	queries   *sqlc.Queries
	protector *installation.Protector
}

var _ CohortVerificationStore = (*PostgresCohortStore)(nil)

// NewPostgresCohortStore requires a protected executor connection and the installation keyring.
func NewPostgresCohortStore(db sqlc.DBTX, protector *installation.Protector) (*PostgresCohortStore, error) {
	if db == nil || protector == nil {
		return nil, ErrInvalidVerifier
	}
	return &PostgresCohortStore{queries: sqlc.New(db), protector: protector}, nil
}

// ClaimCohortTarget decrypts the leased address in memory using the exact binding identity.
func (s *PostgresCohortStore) ClaimCohortTarget(ctx context.Context, applicationID pgtype.UUID, environment Environment, owner pgtype.UUID) (*CohortTargetClaim, error) {
	if !nonzeroUUID(applicationID) || !nonzeroUUID(owner) {
		return nil, ErrInvalidVerifier
	}
	row, err := s.queries.ClaimOnboardingCohortTarget(ctx, sqlc.ClaimOnboardingCohortTargetParams{
		Project: applicationID, Environment: string(environment), Owner: owner,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrVerifierUnavailable
	}
	fence := CohortTargetFence{
		ApplicationID: row.Application, Environment: Environment(row.Environment.String),
		OperationID: row.OperationID, CohortID: row.CohortID, ApprovalID: row.ApprovalID,
		ConfigurationID: row.ConfigurationID, InstallationID: row.InstallationID,
		BindingEpoch: row.BindingEpoch, LeaseOwner: row.TargetOwner, LeaseToken: row.LeaseToken,
	}
	identity := installation.BindingIdentity{
		ApplicationID: row.ApplicationKey, InstallationID: row.InstallationKey, BindingID: row.BindingEpoch,
	}
	address, err := s.protector.Open(identity, row.EncryptionKeyVersion, row.AddressCiphertext)
	if err != nil {
		return nil, ErrVerifierUnavailable
	}
	defer clear(address)
	return &CohortTargetClaim{
		Fence: fence, FileReference: row.FileReference, Project: row.TargetProjectID,
		ServiceAccountEmail: row.ServiceAccountEmail, AddressType: row.AddressType, Address: string(address),
	}, nil
}

// FinishCohortTarget records only allowlisted dry-run evidence under the exact lease.
func (s *PostgresCohortStore) FinishCohortTarget(ctx context.Context, fence CohortTargetFence, evidence CohortTargetEvidence) (bool, error) {
	count, err := s.queries.FinishOnboardingCohortTarget(ctx, sqlc.FinishOnboardingCohortTargetParams{
		Project: fence.ApplicationID, Environment: string(fence.Environment), Operation: fence.OperationID,
		Cohort: fence.CohortID, Approval: fence.ApprovalID, Configuration: fence.ConfigurationID,
		Installation: fence.InstallationID, BindingEpoch: fence.BindingEpoch, Owner: fence.LeaseOwner,
		LeaseToken: fence.LeaseToken, Outcome: evidence.Outcome, FailureCode: evidence.FailureCode,
	})
	if err != nil {
		return false, ErrVerifierUnavailable
	}
	return count == 1, nil
}
