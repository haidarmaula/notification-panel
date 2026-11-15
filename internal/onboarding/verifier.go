package onboarding

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
	"hello/internal/fcm"
)

const verificationTimeout = 50 * time.Second

var (
	ErrInvalidVerifier     = errors.New("invalid onboarding verifier configuration")
	ErrVerifierUnavailable = errors.New("onboarding verification unavailable")
)

// VerificationQueries contains only the database operations needed by the OAuth verifier.
type VerificationQueries interface {
	GetApplicationOnboarding(context.Context, pgtype.UUID) (sqlc.GetApplicationOnboardingRow, error)
	ClaimApplicationOnboardingVerification(context.Context, sqlc.ClaimApplicationOnboardingVerificationParams) (pgtype.UUID, error)
	GetApplicationOnboardingOperation(context.Context, sqlc.GetApplicationOnboardingOperationParams) (sqlc.ApplicationOnboardingOperation, error)
	GetApplicationProviderConfiguration(context.Context, sqlc.GetApplicationProviderConfigurationParams) (sqlc.ApplicationProviderConfiguration, error)
	FinishApplicationOnboardingVerification(context.Context, sqlc.FinishApplicationOnboardingVerificationParams) (bool, error)
}

// Verifier processes one application's OAuth-only verification operations.
type Verifier struct {
	queries        VerificationQueries
	applicationID  pgtype.UUID
	environment    Environment
	credentialRoot string
	leaseOwner     pgtype.UUID
	verify         func(context.Context, fcm.FileCredentialPolicy, string) error
}

// NewVerifier constructs a verifier scoped to one canonical application and environment.
func NewVerifier(queries VerificationQueries, applicationID pgtype.UUID, environment Environment, credentialRoot string) (*Verifier, error) {
	if queries == nil || !applicationID.Valid || applicationID.Bytes == [16]byte{} ||
		(environment != EnvironmentStaging && environment != EnvironmentProduction) ||
		!filepath.IsAbs(credentialRoot) || filepath.Clean(credentialRoot) != credentialRoot || credentialRoot == string(filepath.Separator) {
		return nil, ErrInvalidVerifier
	}
	owner, err := uuid.NewRandom()
	if err != nil {
		return nil, ErrInvalidVerifier
	}
	return &Verifier{
		queries: queries, applicationID: applicationID, environment: environment, credentialRoot: credentialRoot,
		leaseOwner: pgtype.UUID{Bytes: owner, Valid: true}, verify: fcm.VerifyFileCredentials,
	}, nil
}

// RunOnce claims and processes at most one verification operation for the configured app.
func (v *Verifier) RunOnce(ctx context.Context) (bool, error) {
	if v == nil || v.queries == nil || ctx == nil {
		return false, ErrInvalidVerifier
	}
	operationCtx, cancel := context.WithTimeout(ctx, verificationTimeout)
	defer cancel()
	if err := operationCtx.Err(); err != nil {
		return false, ErrVerifierUnavailable
	}
	application, err := v.queries.GetApplicationOnboarding(operationCtx, v.applicationID)
	if err != nil || application.PublicID != v.applicationID || application.ApplicationID <= 0 || !application.Environment.Valid ||
		Environment(application.Environment.String) != v.environment ||
		(application.ApplicationStatus != "PROVISIONING" && application.ApplicationStatus != "ACTIVE") {
		return false, ErrVerifierUnavailable
	}
	operationID, err := v.queries.ClaimApplicationOnboardingVerification(operationCtx, sqlc.ClaimApplicationOnboardingVerificationParams{
		PublicID: v.applicationID, LeaseOwner: v.leaseOwner,
	})
	if err != nil {
		return false, ErrVerifierUnavailable
	}
	if !operationID.Valid || operationID.Bytes == [16]byte{} {
		return false, nil
	}
	operation, err := v.queries.GetApplicationOnboardingOperation(operationCtx, sqlc.GetApplicationOnboardingOperationParams{
		ApplicationID: application.ApplicationID, ID: operationID,
	})
	if err != nil || operation.ID != operationID || operation.ApplicationID != application.ApplicationID ||
		operation.Kind != string(OperationVerify) || operation.State != "RUNNING" || operation.LeaseOwner != v.leaseOwner ||
		operation.LeaseToken <= 0 || !operation.ConfigurationID.Valid || operation.ConfigurationID.Bytes == [16]byte{} {
		return true, ErrVerifierUnavailable
	}
	configuration, err := v.queries.GetApplicationProviderConfiguration(operationCtx, sqlc.GetApplicationProviderConfigurationParams{
		ApplicationID: application.ApplicationID, ID: operation.ConfigurationID,
	})
	if err != nil {
		return true, ErrVerifierUnavailable
	}
	if configuration.ID != operation.ConfigurationID || configuration.ApplicationID != application.ApplicationID ||
		configuration.FileReference != configuration.ID || configuration.Environment != string(v.environment) {
		return v.finish(operationCtx, operation, "INCONCLUSIVE", "FCM_CONFIGURATION_UNAVAILABLE")
	}
	if v.verify == nil {
		return v.finish(operationCtx, operation, "INCONCLUSIVE", "FCM_CONFIGURATION_UNAVAILABLE")
	}
	path := filepath.Join(v.credentialRoot, configuration.ID.String(), "service-account.json")
	policy := fcm.FileCredentialPolicy{
		Path: path, CredentialProjectID: configuration.TargetProjectID, ServiceAccountEmail: configuration.ServiceAccountEmail,
	}
	verificationErr := v.verify(operationCtx, policy, configuration.TargetProjectID)
	outcome, failureCode := verificationOutcome(verificationErr)
	return v.finish(operationCtx, operation, outcome, failureCode)
}

func (v *Verifier) finish(ctx context.Context, operation sqlc.ApplicationOnboardingOperation, outcome, failureCode string) (bool, error) {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	failure := pgtype.Text{}
	if failureCode != "" {
		failure = pgtype.Text{String: failureCode, Valid: true}
	}
	applied, err := v.queries.FinishApplicationOnboardingVerification(finishCtx, sqlc.FinishApplicationOnboardingVerificationParams{
		OperationID: operation.ID, LeaseOwner: v.leaseOwner, LeaseToken: operation.LeaseToken, Outcome: outcome, FailureCode: failure,
	})
	if err != nil || !applied {
		return true, ErrVerifierUnavailable
	}
	return true, nil
}

func verificationOutcome(err error) (string, string) {
	switch {
	case err == nil:
		return "PASSED", ""
	case errors.Is(err, fcm.ErrCredentials):
		return "FAILED", "FCM_CREDENTIAL_REJECTED"
	case errors.Is(err, fcm.ErrVerificationTemporary):
		return "INCONCLUSIVE", "FCM_CHECK_INCONCLUSIVE"
	default:
		return "INCONCLUSIVE", "FCM_CONFIGURATION_UNAVAILABLE"
	}
}
