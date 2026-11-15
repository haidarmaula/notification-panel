package onboarding

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
	"hello/internal/fcm"
)

const (
	verifierTestApplicationID = "11111111-1111-4111-8111-111111111111"
	verifierTestOperationID   = "22222222-2222-4222-8222-222222222222"
	verifierTestConfigID      = "33333333-3333-4333-8333-333333333333"
)

type verificationQueriesFixture struct {
	application  sqlc.GetApplicationOnboardingRow
	operationID  pgtype.UUID
	operation    sqlc.ApplicationOnboardingOperation
	config       sqlc.ApplicationProviderConfiguration
	finish       sqlc.FinishApplicationOnboardingVerificationParams
	finishCalls  int
	finishResult bool
	appErr       error
	claimErr     error
	operationErr error
	configErr    error
	finishErr    error
}

func (q *verificationQueriesFixture) GetApplicationOnboarding(context.Context, pgtype.UUID) (sqlc.GetApplicationOnboardingRow, error) {
	return q.application, q.appErr
}
func (q *verificationQueriesFixture) ClaimApplicationOnboardingVerification(context.Context, sqlc.ClaimApplicationOnboardingVerificationParams) (pgtype.UUID, error) {
	return q.operationID, q.claimErr
}
func (q *verificationQueriesFixture) GetApplicationOnboardingOperation(context.Context, sqlc.GetApplicationOnboardingOperationParams) (sqlc.ApplicationOnboardingOperation, error) {
	return q.operation, q.operationErr
}
func (q *verificationQueriesFixture) GetApplicationProviderConfiguration(context.Context, sqlc.GetApplicationProviderConfigurationParams) (sqlc.ApplicationProviderConfiguration, error) {
	return q.config, q.configErr
}
func (q *verificationQueriesFixture) FinishApplicationOnboardingVerification(_ context.Context, arg sqlc.FinishApplicationOnboardingVerificationParams) (bool, error) {
	q.finishCalls++
	q.finish = arg
	return q.finishResult, q.finishErr
}

func newVerificationFixture(t *testing.T) (*Verifier, *verificationQueriesFixture) {
	t.Helper()
	appID, err := fixtureVerifierUUID(verifierTestApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	operationID, err := fixtureVerifierUUID(verifierTestOperationID)
	if err != nil {
		t.Fatal(err)
	}
	configID, err := fixtureVerifierUUID(verifierTestConfigID)
	if err != nil {
		t.Fatal(err)
	}
	ownerID, err := fixtureVerifierUUID("44444444-4444-4444-8444-444444444444")
	if err != nil {
		t.Fatal(err)
	}
	queries := &verificationQueriesFixture{
		application: sqlc.GetApplicationOnboardingRow{
			PublicID: appID, ApplicationID: 17, ApplicationStatus: "PROVISIONING",
			Environment: pgtype.Text{String: string(EnvironmentStaging), Valid: true},
		},
		operationID: operationID,
		operation: sqlc.ApplicationOnboardingOperation{
			ID: operationID, ApplicationID: 17, Kind: string(OperationVerify), State: "RUNNING",
			LeaseOwner: ownerID, LeaseToken: 8, ConfigurationID: configID,
		},
		config: sqlc.ApplicationProviderConfiguration{
			ID: configID, ApplicationID: 17, FileReference: configID,
			Environment: string(EnvironmentStaging), TargetProjectID: "provider-project",
			ServiceAccountEmail: "svc@provider-project.iam.gserviceaccount.com",
		},
		finishResult: true,
	}
	verifier, err := NewVerifier(queries, appID, EnvironmentStaging, "/etc/hello/credentials")
	if err != nil {
		t.Fatal(err)
	}
	queries.operation.LeaseOwner = verifier.leaseOwner
	return verifier, queries
}

func fixtureVerifierUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if id.Scan(value) != nil || !id.Valid || id.Bytes == [16]byte{} || id.String() != value {
		return id, ErrInvalidVerifier
	}
	return id, nil
}

func TestVerifierRunsOAuthOnlyAndFencesFinish(t *testing.T) {
	verifier, queries := newVerificationFixture(t)
	called := false
	verifier.verify = func(ctx context.Context, policy fcm.FileCredentialPolicy, target string) error {
		called = true
		if ctx == nil || target != "provider-project" || policy.CredentialProjectID != target ||
			policy.ServiceAccountEmail != "svc@provider-project.iam.gserviceaccount.com" ||
			policy.Path != filepath.Join("/etc/hello/credentials", verifierTestConfigID, "service-account.json") {
			t.Fatalf("unexpected verification request: policy=%#v target=%q", policy, target)
		}
		return nil
	}
	worked, err := verifier.RunOnce(context.Background())
	if err != nil || !worked || !called {
		t.Fatalf("RunOnce() = (%t, %v), verifier called=%t", worked, err, called)
	}
	if queries.finishCalls != 1 || queries.finish.OperationID != queries.operationID || queries.finish.LeaseOwner != verifier.leaseOwner ||
		queries.finish.LeaseToken != 8 || queries.finish.Outcome != "PASSED" || queries.finish.FailureCode.Valid {
		t.Fatalf("unexpected fenced finish: %#v", queries.finish)
	}
}

func TestVerifierMapsCredentialOutcomesToSafeCodes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verify  error
		outcome string
		failure string
	}{
		{name: "credential rejected", verify: fcm.ErrCredentials, outcome: "FAILED", failure: "FCM_CREDENTIAL_REJECTED"},
		{name: "temporary OAuth failure", verify: fcm.ErrVerificationTemporary, outcome: "INCONCLUSIVE", failure: "FCM_CHECK_INCONCLUSIVE"},
		{name: "configuration failure", verify: fcm.ErrConfiguration, outcome: "INCONCLUSIVE", failure: "FCM_CONFIGURATION_UNAVAILABLE"},
		{name: "unknown error is sanitized", verify: errors.New("sensitive provider response"), outcome: "INCONCLUSIVE", failure: "FCM_CONFIGURATION_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifier, queries := newVerificationFixture(t)
			verifier.verify = func(context.Context, fcm.FileCredentialPolicy, string) error { return tc.verify }
			worked, err := verifier.RunOnce(context.Background())
			if err != nil || !worked {
				t.Fatalf("RunOnce() = (%t, %v)", worked, err)
			}
			if queries.finishCalls != 1 || queries.finish.Outcome != tc.outcome ||
				!queries.finish.FailureCode.Valid || queries.finish.FailureCode.String != tc.failure {
				t.Fatalf("unexpected finish result: %#v", queries.finish)
			}
		})
	}
}

func TestVerifierReturnsIdleWithoutReadingOperation(t *testing.T) {
	verifier, queries := newVerificationFixture(t)
	queries.operationID = pgtype.UUID{}
	verifier.verify = func(context.Context, fcm.FileCredentialPolicy, string) error {
		t.Fatal("verification called without a claimed operation")
		return nil
	}
	worked, err := verifier.RunOnce(context.Background())
	if err != nil || worked || queries.finishCalls != 0 {
		t.Fatalf("RunOnce() = (%t, %v), finish calls=%d", worked, err, queries.finishCalls)
	}
}

func TestVerifierRejectsMismatchedConfigurationBeforeReadingSecret(t *testing.T) {
	verifier, queries := newVerificationFixture(t)
	queries.config.FileReference = pgtype.UUID{}
	called := false
	verifier.verify = func(context.Context, fcm.FileCredentialPolicy, string) error {
		called = true
		return nil
	}
	worked, err := verifier.RunOnce(context.Background())
	if err != nil || !worked || called || queries.finishCalls != 1 || queries.finish.Outcome != "INCONCLUSIVE" ||
		queries.finish.FailureCode.String != "FCM_CONFIGURATION_UNAVAILABLE" {
		t.Fatalf("RunOnce()=(%t,%v) called=%t finish=%#v", worked, err, called, queries.finish)
	}
}

func TestVerifierReportsLostLeaseWhenFinishIsNotApplied(t *testing.T) {
	verifier, queries := newVerificationFixture(t)
	queries.finishResult = false
	verifier.verify = func(context.Context, fcm.FileCredentialPolicy, string) error { return nil }
	worked, err := verifier.RunOnce(context.Background())
	if !worked || !errors.Is(err, ErrVerifierUnavailable) || queries.finishCalls != 1 {
		t.Fatalf("RunOnce() = (%t, %v), finish calls=%d", worked, err, queries.finishCalls)
	}
}

func TestVerifierConstructorRequiresFixedScope(t *testing.T) {
	appID, _ := fixtureVerifierUUID(verifierTestApplicationID)
	for _, tc := range []struct {
		name string
		id   pgtype.UUID
		env  Environment
		root string
	}{
		{name: "zero application", id: pgtype.UUID{}, env: EnvironmentStaging, root: "/etc/hello/credentials"},
		{name: "unsupported environment", id: appID, env: "development", root: "/etc/hello/credentials"},
		{name: "relative root", id: appID, env: EnvironmentStaging, root: "credentials"},
		{name: "unclean root", id: appID, env: EnvironmentStaging, root: "/etc/hello/../credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewVerifier(&verificationQueriesFixture{}, tc.id, tc.env, tc.root); !errors.Is(err, ErrInvalidVerifier) {
				t.Fatalf("NewVerifier() error = %v", err)
			}
		})
	}
}
