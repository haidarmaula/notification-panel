package applications

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/onboarding"
	"hello/internal/provisioning"
)

type forbiddenStager struct{}

func (forbiddenStager) Stage(context.Context, provisioning.StageRequest) (provisioning.StageResult, error) {
	panic("unexpected credential publication")
}
func setupSettings() OnboardingSettings {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return OnboardingSettings{FingerprintKey: key, FingerprintKeyVersion: 1, Stager: forbiddenStager{}}
}
func TestOnboardingCurrentAuthorityPrecedesTargetsAndCredentials(t *testing.T) {
	for _, role := range []string{"VIEWER", "OPERATOR", "ADMIN", "UNKNOWN"} {
		t.Run(role, func(t *testing.T) {
			service, err := NewOnboardingService(principalQueries{role: role, active: true}, panicDB{}, setupSettings())
			require.NoError(t, err)
			id, _ := onboardingID("11111111-1111-4111-8111-111111111111")
			operations := []func() error{
				func() error { _, err := service.Readiness(staffContext(), id); return err },
				func() error { _, err := service.Profiles(staffContext(), id); return err },
				func() error { _, err := service.Approvals(staffContext(), id); return err },
				func() error { _, err := service.Operation(staffContext(), id, id); return err },
				func() error {
					_, _, err := service.StoreFCM(staffContext(), id, onboarding.FCMRequest{}, OnboardingPreconditions{})
					return err
				},
				func() error {
					_, _, err := service.ConfigureEnrollment(staffContext(), id, onboarding.EnrollmentRequest{}, OnboardingPreconditions{})
					return err
				},
				func() error {
					_, _, err := service.Queue(staffContext(), id, OnboardingCommand{}, OnboardingPreconditions{})
					return err
				},
				func() error {
					_, _, err := service.Hold(staffContext(), id, onboarding.HoldRequest{}, OnboardingPreconditions{})
					return err
				},
			}
			for _, op := range operations {
				require.ErrorIs(t, op(), authz.ErrForbidden)
			}
		})
	}
}
func TestOnboardingSettingsAreCopiedAndWeakFingerprintKeysRejected(t *testing.T) {
	settings := setupSettings()
	service, err := NewOnboardingService(principalQueries{}, panicDB{}, settings)
	require.NoError(t, err)
	before, err := service.fingerprint(map[string]string{"value": "fixture"})
	require.NoError(t, err)
	settings.FingerprintKey[0]++
	after, err := service.fingerprint(map[string]string{"value": "fixture"})
	require.NoError(t, err)
	require.Equal(t, before, after)
	settings.FingerprintKey = []byte(strings.Repeat("x", 32))
	_, err = NewOnboardingService(principalQueries{}, panicDB{}, settings)
	require.ErrorIs(t, err, ErrOnboardingUnavailable)
}
func TestOnboardingFingerprintMatchesJSONSemanticsWithoutExposingInput(t *testing.T) {
	service, err := NewOnboardingService(principalQueries{}, panicDB{}, setupSettings())
	require.NoError(t, err)
	a := onboarding.FCMRequest{Environment: onboarding.EnvironmentStaging, TargetProjectID: "fixture-project", ServiceAccount: []byte(`{"client_email":"a@example.invalid","private_key":"sensitive-fixture"}`)}
	b := a
	b.ServiceAccount = []byte(`{ "private_key": "sensitive-fixture", "client_email": "a@example.invalid" }`)
	first, err := service.fingerprint(a)
	require.NoError(t, err)
	second, err := service.fingerprint(b)
	require.NoError(t, err)
	require.Equal(t, first, second)
	b.TargetProjectID = "another-project"
	second, err = service.fingerprint(b)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
}
func TestOnboardingConditionsBindApplicationAndRevision(t *testing.T) {
	id, _ := onboardingID("11111111-1111-4111-8111-111111111111")
	key := "22222222-2222-4222-8222-222222222222"
	for _, tc := range []struct {
		tag  string
		want error
	}{
		{"", ErrPreconditionRequired}, {`W/"onboarding:11111111-1111-4111-8111-111111111111:2"`, ErrInvalidPrecondition},
		{`"onboarding:11111111-1111-4111-8111-111111111111:1"`, ErrPreconditionFailed},
		{`"onboarding:33333333-3333-4333-8333-333333333333:2"`, ErrInvalidPrecondition},
		{`"onboarding:11111111-1111-4111-8111-111111111111:2"`, nil},
	} {
		_, err := onboardingCondition(id, 2, OnboardingPreconditions{IfMatch: tc.tag, IdempotencyKey: key})
		require.ErrorIs(t, err, tc.want)
	}
}

type onboardingReads struct {
	principalQueries
	row       sqlc.GetApplicationOnboardingRow
	checks    []sqlc.ApplicationProviderCheck
	runtime   sqlc.LockNotificationRuntimeRow
	lastError error
}

func (q onboardingReads) GetApplicationOnboarding(context.Context, pgtype.UUID) (sqlc.GetApplicationOnboardingRow, error) {
	return q.row, nil
}
func (q onboardingReads) ListApplicationProviderChecks(context.Context, sqlc.ListApplicationProviderChecksParams) ([]sqlc.ApplicationProviderCheck, error) {
	return q.checks, nil
}
func (q onboardingReads) LockNotificationRuntime(context.Context) (sqlc.LockNotificationRuntimeRow, error) {
	return q.runtime, nil
}
func (q onboardingReads) GetLastApplicationOnboardingOperation(context.Context, int64) (sqlc.ApplicationOnboardingOperation, error) {
	return sqlc.ApplicationOnboardingOperation{}, q.lastError
}
func TestOnboardingReadinessDoesNotConfuseActivationWithDispatch(t *testing.T) {
	id, _ := onboardingID("11111111-1111-4111-8111-111111111111")
	q := onboardingReads{principalQueries: principalQueries{role: "SUPER_ADMIN", active: true}, row: sqlc.GetApplicationOnboardingRow{PublicID: id, ApplicationID: 1, Revision: 5, ApplicationStatus: "ACTIVE", DispatchMode: "HELD"}, lastError: pgx.ErrNoRows}
	service, err := NewOnboardingService(q, panicDB{}, setupSettings())
	require.NoError(t, err)
	got, err := service.Readiness(staffContext(), id)
	require.NoError(t, err)
	require.Equal(t, onboarding.ApplicationActive, got.ApplicationStatus)
	require.Equal(t, onboarding.DispatchHeld, got.DispatchMode)
	require.Contains(t, got.Blockers, "DISPATCH_APPROVAL_REQUIRED")
	require.NotNil(t, got.Checks)
	require.NotNil(t, got.NextActions)
	q.lastError = errors.New("database secret fixture")
	service, err = NewOnboardingService(q, panicDB{}, setupSettings())
	require.NoError(t, err)
	_, err = service.Readiness(staffContext(), id)
	require.Error(t, err)
}

func TestOnboardingReadinessScopesReplacementEvidenceToCandidate(t *testing.T) {
	appID, _ := onboardingID("11111111-1111-4111-8111-111111111111")
	current, _ := onboardingID("22222222-2222-4222-8222-222222222222")
	candidate, _ := onboardingID("33333333-3333-4333-8333-333333333333")
	q := onboardingReads{
		principalQueries: principalQueries{role: "SUPER_ADMIN", active: true},
		row: sqlc.GetApplicationOnboardingRow{
			PublicID: appID, ApplicationID: 1, Revision: 7, ApplicationStatus: "ACTIVE", DispatchMode: "FULL",
			CurrentConfigurationID: current, CandidateConfigurationID: candidate,
		},
		lastError: pgx.ErrNoRows,
	}
	service, err := NewOnboardingService(q, panicDB{}, setupSettings())
	require.NoError(t, err)
	got, err := service.Readiness(staffContext(), appID)
	require.NoError(t, err)
	require.Equal(t, current.String(), *got.CurrentConfigurationID)
	require.Equal(t, candidate.String(), *got.ChecksConfigurationID)
	require.Equal(t, onboarding.DispatchFull, got.DispatchMode)
	require.Len(t, got.Checks, 4)
	for _, check := range got.Checks {
		require.Equal(t, onboarding.CheckNotChecked, check.Outcome)
	}
	q.row.CandidateConfigurationID = pgtype.UUID{}
	service, err = NewOnboardingService(q, panicDB{}, setupSettings())
	require.NoError(t, err)
	got, err = service.Readiness(staffContext(), appID)
	require.NoError(t, err)
	require.Equal(t, current.String(), *got.ChecksConfigurationID)
}

func TestOnboardingReadinessMarksStaleOAuthChecksInconclusive(t *testing.T) {
	id, _ := onboardingID("11111111-1111-4111-8111-111111111111")
	configID, _ := onboardingID("22222222-2222-4222-8222-222222222222")
	origin := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	source := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	now := time.Now()
	for _, tc := range []struct {
		name    string
		checked sqlc.ApplicationProviderCheck
		fresh   bool
	}{
		{
			name: "fresh",
			checked: sqlc.ApplicationProviderCheck{
				Kind: "OAUTH", Outcome: "PASSED", OriginEpoch: origin, SourceEpoch: source,
				CheckedAt: pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true},
				ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true},
			},
			fresh: true,
		},
		{
			name: "expired",
			checked: sqlc.ApplicationProviderCheck{
				Kind: "OAUTH", Outcome: "PASSED", OriginEpoch: origin, SourceEpoch: source,
				CheckedAt: pgtype.Timestamptz{Time: now.Add(-2 * time.Hour), Valid: true},
				ExpiresAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
			},
		},
		{
			name: "wrong origin epoch",
			checked: sqlc.ApplicationProviderCheck{
				Kind: "OAUTH", Outcome: "PASSED", OriginEpoch: pgtype.UUID{Bytes: [16]byte{9}, Valid: true}, SourceEpoch: source,
				CheckedAt: pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true},
				ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true},
			},
		},
		{
			name: "wrong source epoch",
			checked: sqlc.ApplicationProviderCheck{
				Kind: "OAUTH", Outcome: "PASSED", OriginEpoch: origin, SourceEpoch: pgtype.UUID{Bytes: [16]byte{9}, Valid: true},
				CheckedAt: pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true},
				ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := onboardingReads{
				principalQueries: principalQueries{role: "SUPER_ADMIN", active: true},
				row: sqlc.GetApplicationOnboardingRow{
					PublicID: id, ApplicationID: 1, Revision: 5, ApplicationStatus: "PROVISIONING",
					DispatchMode: "HELD", CandidateConfigurationID: configID,
				},
				checks:    []sqlc.ApplicationProviderCheck{tc.checked},
				runtime:   sqlc.LockNotificationRuntimeRow{OriginEpoch: origin, SourceEpoch: source},
				lastError: pgx.ErrNoRows,
			}
			service, err := NewOnboardingService(q, panicDB{}, setupSettings())
			require.NoError(t, err)
			got, err := service.Readiness(staffContext(), id)
			require.NoError(t, err)
			require.Len(t, got.Checks, 4)
			require.Equal(t, configID.String(), *got.ChecksConfigurationID)
			for _, check := range got.Checks {
				if check.Code == "TARGET_PERMISSION" || check.Code == "DEVICE_DELIVERY" {
					require.Equal(t, onboarding.CheckNotChecked, check.Outcome)
					require.Nil(t, check.CheckedAt)
					require.Nil(t, check.ExpiresAt)
				}
			}
			if tc.fresh {
				require.Equal(t, onboarding.CheckPassed, got.Checks[0].Outcome)
				require.NotContains(t, got.Blockers, "FCM_NOT_VERIFIED")
				require.NotContains(t, got.NextActions, "VERIFY_FCM")
				return
			}
			require.Equal(t, onboarding.CheckInconclusive, got.Checks[0].Outcome)
			require.Contains(t, got.Blockers, "FCM_NOT_VERIFIED")
			require.Contains(t, got.NextActions, "VERIFY_FCM")
		})
	}
}

func TestOnboardingSettingsCopyProfileAliases(t *testing.T) {
	settings := setupSettings()
	platforms := []onboarding.Platform{onboarding.PlatformAndroid}
	identifiers := []string{"com.example.original"}
	settings.Profiles = []OnboardingProfile{{
		Profile:                onboarding.Profile{ID: "11111111-1111-4111-8111-111111111111", Name: "Fixture", Platforms: platforms},
		ApplicationIdentifiers: map[onboarding.Platform][]string{onboarding.PlatformAndroid: identifiers},
	}}
	service, err := NewOnboardingService(principalQueries{}, panicDB{}, settings)
	require.NoError(t, err)

	platforms[0] = onboarding.PlatformIOS
	identifiers[0] = "com.example.mutated"
	settings.Profiles[0].ApplicationIdentifiers[onboarding.PlatformAndroid][0] = "com.example.settings-mutated"
	require.Equal(t, []onboarding.Platform{onboarding.PlatformAndroid}, service.settings.Profiles[0].Profile.Platforms)
	require.Equal(t, []string{"com.example.original"}, service.settings.Profiles[0].ApplicationIdentifiers[onboarding.PlatformAndroid])
}

func TestOperationViewContainsSafeOutcomeAndStringRevision(t *testing.T) {
	id, _ := onboardingID("11111111-1111-4111-8111-111111111111")
	op := operationView(sqlc.ApplicationOnboardingOperation{ID: id, Kind: "VERIFY", State: "SUCCEEDED", CreatedAt: pgtype.Timestamptz{Time: time.Unix(1, 0), Valid: true}, ResultingRevision: pgtype.Int8{Int64: 9007199254740993, Valid: true}})
	require.Equal(t, "9007199254740993", *op.ResultingRevision)
}

func (q onboardingReads) HasApplicationOnboardingVerifier(context.Context, int64) (bool, error) {
	return true, nil
}

func TestOnboardingSubmissionReplaySnapshotsDoNotDrift(t *testing.T) {
	id, _ := onboardingID("11111111-1111-4111-8111-111111111111")
	stored := uploadCandidateView(sqlc.ApplicationProviderConfiguration{ID: id, State: "VERIFIED", TargetProjectID: "fixture-project", ServiceAccountEmail: "fixture@example.invalid"}, 2)
	require.Equal(t, onboarding.CandidateStored, stored.State)
	op := queuedOperationView(sqlc.ApplicationOnboardingOperation{ID: id, Kind: "VERIFY", State: "SUCCEEDED", CreatedAt: pgtype.Timestamptz{Time: time.Unix(1, 0), Valid: true}, CompletedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}})
	require.Equal(t, onboarding.OperationPending, op.State)
	require.Nil(t, op.CompletedAt)
	require.Nil(t, op.ResultingRevision)
}
