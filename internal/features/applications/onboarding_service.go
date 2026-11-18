package applications

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/fcm"
	"hello/internal/onboarding"
	"hello/internal/provisioning"
)

var (
	ErrOnboardingUnavailable = errors.New("application onboarding unavailable")
	ErrOnboardingConflict    = errors.New("application onboarding state conflict")
	ErrOnboardingRateLimited = errors.New("application onboarding rate limit exceeded")
)

// CredentialStager is an authenticated, scoped connection to the host provisioner.
// Its implementation must never persist credential bodies in an operation queue.
type CredentialStager interface {
	Stage(context.Context, provisioning.StageRequest) (provisioning.StageResult, error)
}

// OnboardingProfile is an operator-approved enrollment template. Application
// identifiers are allowlisted independently of a caller's Firebase credential.
type OnboardingProfile struct {
	Profile                onboarding.Profile
	ApplicationIdentifiers map[onboarding.Platform][]string
}

// OnboardingSettings is fixed at startup, never selected by an HTTP request.
type OnboardingSettings struct {
	FingerprintKey        []byte
	FingerprintKeyVersion int32
	Profiles              []OnboardingProfile
	Stager                CredentialStager
}

// OnboardingService owns staff-authorized setup intent. It never changes the
// application lifecycle or releases provider dispatch using its runtime login.
type OnboardingService struct {
	base     *Service
	settings OnboardingSettings
}

func NewOnboardingService(q sqlc.Querier, db Transactor, settings OnboardingSettings) (*OnboardingService, error) {
	if q == nil || db == nil || settings.Stager == nil || len(settings.FingerprintKey) != 32 || settings.FingerprintKeyVersion <= 0 || len(settings.Profiles) > 100 {
		return nil, ErrOnboardingUnavailable
	}
	distinct := map[byte]bool{}
	for _, b := range settings.FingerprintKey {
		distinct[b] = true
	}
	if len(distinct) < 8 {
		return nil, ErrOnboardingUnavailable
	}
	settings.FingerprintKey = append([]byte(nil), settings.FingerprintKey...)
	profiles := make([]OnboardingProfile, 0, len(settings.Profiles))
	seen := map[string]bool{}
	for _, profile := range settings.Profiles {
		id, err := onboardingID(profile.Profile.ID)
		if err != nil || id.String() != profile.Profile.ID || seen[id.String()] || strings.TrimSpace(profile.Profile.Name) == "" || len(profile.Profile.Name) > 128 || !utf8.ValidString(profile.Profile.Name) || strings.IndexFunc(profile.Profile.Name, unicode.IsControl) >= 0 || len(profile.Profile.Platforms) == 0 || len(profile.Profile.Platforms) > 2 {
			return nil, ErrOnboardingUnavailable
		}
		seen[id.String()] = true
		seenPlatforms := map[onboarding.Platform]bool{}
		for _, platform := range profile.Profile.Platforms {
			if (platform != onboarding.PlatformAndroid && platform != onboarding.PlatformIOS) || seenPlatforms[platform] {
				return nil, ErrOnboardingUnavailable
			}
			seenPlatforms[platform] = true
			identifiers := profile.ApplicationIdentifiers[platform]
			if len(identifiers) < 1 || len(identifiers) > 100 {
				return nil, ErrOnboardingUnavailable
			}
			seenIDs := map[string]bool{}
			for _, identifier := range identifiers {
				if len(identifier) < 1 || len(identifier) > 255 || !utf8.ValidString(identifier) || strings.IndexFunc(identifier, unicode.IsControl) >= 0 || seenIDs[identifier] {
					return nil, ErrOnboardingUnavailable
				}
				seenIDs[identifier] = true
			}
		}
		if len(profile.ApplicationIdentifiers) != len(seenPlatforms) {
			return nil, ErrOnboardingUnavailable
		}
		copyProfile := OnboardingProfile{Profile: profile.Profile, ApplicationIdentifiers: map[onboarding.Platform][]string{}}
		copyProfile.Profile.Platforms = append([]onboarding.Platform(nil), profile.Profile.Platforms...)
		for platform, ids := range profile.ApplicationIdentifiers {
			copyProfile.ApplicationIdentifiers[platform] = append([]string(nil), ids...)
		}
		profiles = append(profiles, copyProfile)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Profile.ID < profiles[j].Profile.ID })
	settings.Profiles = profiles
	return &OnboardingService{base: NewService(q, db), settings: settings}, nil
}

func onboardingID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if len(value) != 36 || value != strings.ToLower(value) || id.Scan(value) != nil || !id.Valid || id.Bytes == [16]byte{} || id.String() != value {
		return id, ErrInvalidID
	}
	return id, nil
}
func optionalOnboardingID(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	value := id.String()
	return &value
}
func onboardingTime(value pgtype.Timestamptz) *string {
	if !value.Valid {
		return nil
	}
	s := value.Time.UTC().Format(time.RFC3339Nano)
	return &s
}
func operationView(op sqlc.ApplicationOnboardingOperation) onboarding.Operation {
	result := onboarding.Operation{ID: op.ID.String(), Kind: onboarding.OperationKind(op.Kind), State: onboarding.OperationState(op.State), CreatedAt: op.CreatedAt.Time.UTC().Format(time.RFC3339Nano), CompletedAt: onboardingTime(op.CompletedAt)}
	if op.FailureCode.Valid {
		result.FailureCode = &op.FailureCode.String
	}
	if op.ResultingRevision.Valid {
		value := strconv.FormatInt(op.ResultingRevision.Int64, 10)
		result.ResultingRevision = &value
	}
	return result
}
func setupView(row sqlc.GetApplicationOnboardingRow) onboarding.Readiness {
	result := onboarding.Readiness{ApplicationID: row.PublicID.String(), ApplicationStatus: onboarding.ApplicationStatus(row.ApplicationStatus), DispatchMode: onboarding.DispatchMode(row.DispatchMode), Revision: strconv.FormatInt(row.Revision, 10), CurrentConfigurationID: optionalOnboardingID(row.CurrentConfigurationID), CandidateConfigurationID: optionalOnboardingID(row.CandidateConfigurationID), EnrollmentProfileID: optionalOnboardingID(row.EnrollmentProfileID), CohortID: optionalOnboardingID(row.CohortID), Checks: []onboarding.ReadinessCheck{}, Blockers: []string{}, NextActions: []string{}}
	result.ChecksConfigurationID = result.CandidateConfigurationID
	if result.ChecksConfigurationID == nil {
		result.ChecksConfigurationID = result.CurrentConfigurationID
	}
	if row.Environment.Valid {
		v := onboarding.Environment(row.Environment.String)
		result.Environment = &v
	}
	if !row.CandidateConfigurationID.Valid && !row.CurrentConfigurationID.Valid {
		result.Blockers = append(result.Blockers, "FCM_NOT_CONFIGURED")
		result.NextActions = append(result.NextActions, "CONFIGURE_FCM")
	}
	if !row.EnrollmentProfileID.Valid {
		result.Blockers = append(result.Blockers, "ENROLLMENT_NOT_CONFIGURED")
		result.NextActions = append(result.NextActions, "CONFIGURE_ENROLLMENT")
	}
	if row.DispatchMode == "HELD" {
		result.Blockers = append(result.Blockers, "DISPATCH_APPROVAL_REQUIRED")
	}
	if row.ApplicationStatus == "PROVISIONING" {
		result.Blockers = append(result.Blockers, "RUNTIME_PENDING")
		result.NextActions = append(result.NextActions, "PREPARE_RUNTIME")
	}
	if !row.Greenfield && !row.CurrentConfigurationID.Valid {
		result.Blockers = append(result.Blockers, "LEGACY_ADOPTION_REQUIRED")
		result.NextActions = append(result.NextActions, "REVIEW_EXISTING_CONFIGURATION")
	}
	return result
}

func (s *OnboardingService) managed(ctx context.Context, id pgtype.UUID) (sqlc.GetApplicationOnboardingRow, error) {
	if _, err := s.base.require(ctx, authz.ApplicationsManage); err != nil {
		return sqlc.GetApplicationOnboardingRow{}, err
	}
	if err := validID(id); err != nil {
		return sqlc.GetApplicationOnboardingRow{}, err
	}
	row, err := s.base.q.GetApplicationOnboarding(ctx, id)
	return row, appError(err)
}
func (s *OnboardingService) Readiness(ctx context.Context, id pgtype.UUID) (onboarding.Readiness, error) {
	row, err := s.managed(ctx, id)
	if err != nil {
		return onboarding.Readiness{}, err
	}
	result := setupView(row)
	config := row.CandidateConfigurationID
	if !config.Valid {
		config = row.CurrentConfigurationID
	}
	if config.Valid {
		checks, err := s.base.q.ListApplicationProviderChecks(ctx, sqlc.ListApplicationProviderChecksParams{ApplicationID: row.ApplicationID, ConfigurationID: config})
		if err != nil {
			return result, err
		}
		runtime, err := s.base.q.LockNotificationRuntime(ctx)
		if err != nil {
			return result, err
		}
		freshOAuth := false
		for _, c := range checks {
			outcome := onboarding.CheckOutcome(c.Outcome)
			fresh := c.ExpiresAt.Time.After(time.Now()) && c.OriginEpoch == runtime.OriginEpoch && c.SourceEpoch == runtime.SourceEpoch
			if outcome == onboarding.CheckPassed && !fresh {
				outcome = onboarding.CheckInconclusive
			}
			if c.Kind == "OAUTH" && outcome == onboarding.CheckPassed {
				freshOAuth = true
			}
			result.Checks = append(result.Checks, onboarding.ReadinessCheck{Code: c.Kind, Outcome: outcome, CheckedAt: onboardingTime(c.CheckedAt), ExpiresAt: onboardingTime(c.ExpiresAt)})
		}
		if !freshOAuth {
			result.Blockers = append(result.Blockers, "FCM_NOT_VERIFIED")
			available, err := s.base.q.HasApplicationOnboardingVerifier(ctx, row.ApplicationID)
			if err != nil {
				return result, err
			}
			if available {
				result.NextActions = append(result.NextActions, "VERIFY_FCM")
			} else {
				result.Blockers = append(result.Blockers, "VERIFIER_NOT_CONFIGURED")
				result.NextActions = append(result.NextActions, "CONFIGURE_VERIFIER")
			}
		}
	}
	// Missing evidence is explicit; OAuth success cannot fill permission or device checks.
	for _, kind := range []string{"LOCAL_FORMAT", "OAUTH", "TARGET_PERMISSION", "DEVICE_DELIVERY"} {
		found := false
		for _, check := range result.Checks {
			if check.Code == kind {
				found = true
				break
			}
		}
		if !found {
			result.Checks = append(result.Checks, onboarding.ReadinessCheck{Code: kind, Outcome: onboarding.CheckNotChecked})
		}
	}
	op, err := s.base.q.GetLastApplicationOnboardingOperation(ctx, row.ApplicationID)
	if err == nil {
		view := operationView(op)
		result.LastOperation = &view
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	return result, nil
}
func (s *OnboardingService) Profiles(ctx context.Context, id pgtype.UUID) ([]onboarding.Profile, error) {
	if _, err := s.managed(ctx, id); err != nil {
		return nil, err
	}
	results := make([]onboarding.Profile, 0, len(s.settings.Profiles))
	for _, p := range s.settings.Profiles {
		copy := p.Profile
		copy.Platforms = append([]onboarding.Platform(nil), copy.Platforms...)
		results = append(results, copy)
	}
	return results, nil
}
func (s *OnboardingService) Approvals(ctx context.Context, id pgtype.UUID) ([]onboarding.Approval, error) {
	row, err := s.managed(ctx, id)
	if err != nil {
		return nil, err
	}
	approvals, err := s.base.q.ListApplicationOnboardingApprovals(ctx, row.ApplicationID)
	if err != nil {
		return nil, err
	}
	if len(approvals) > 100 {
		return nil, ErrOnboardingUnavailable
	}
	results := make([]onboarding.Approval, 0, len(approvals))
	for _, a := range approvals {
		status := onboarding.ApprovalStatus("VALID")
		if a.RevokedAt.Valid {
			status = "REVOKED"
		} else if !a.ExpiresAt.Time.After(time.Now()) {
			status = "EXPIRED"
		}
		results = append(results, onboarding.Approval{ID: a.ID.String(), Kind: onboarding.ApprovalKind(a.Kind), Status: status, ExpiresAt: a.ExpiresAt.Time.UTC().Format(time.RFC3339Nano)})
	}
	return results, nil
}
func (s *OnboardingService) Operation(ctx context.Context, id, operationID pgtype.UUID) (onboarding.Operation, error) {
	row, err := s.managed(ctx, id)
	if err != nil {
		return onboarding.Operation{}, err
	}
	if err := validID(operationID); err != nil {
		return onboarding.Operation{}, err
	}
	operation, err := s.base.q.GetApplicationOnboardingOperation(ctx, sqlc.GetApplicationOnboardingOperationParams{ApplicationID: row.ApplicationID, ID: operationID})
	return operationView(operation), appError(err)
}
func (s *OnboardingService) fingerprint(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var canonical any
	if json.Unmarshal(data, &canonical) != nil {
		return nil, onboarding.ErrInvalidRequest
	}
	data, err = json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	hash := hmac.New(sha256.New, s.settings.FingerprintKey)
	_, _ = hash.Write(data)
	return hash.Sum(nil), nil
}
func onboardingCondition(id pgtype.UUID, revision int64, pre OnboardingPreconditions) (pgtype.UUID, error) {
	key, err := onboardingID(pre.IdempotencyKey)
	if err != nil {
		return key, ErrInvalidPrecondition
	}
	if pre.IfMatch == "" {
		return key, ErrPreconditionRequired
	}
	if !validOnboardingETag(pre.IfMatch, id) {
		return key, ErrInvalidPrecondition
	}
	if pre.IfMatch != fmt.Sprintf("\"onboarding:%s:%d\"", id.String(), revision) {
		return key, ErrPreconditionFailed
	}
	return key, nil
}
func (s *OnboardingService) mutate(ctx context.Context, id pgtype.UUID, fn func(context.Context, *sqlc.Queries, sqlc.Application, staffWriteSession, pgtype.UUID, sqlc.ApplicationOnboarding) error) error {
	if _, err := s.base.require(ctx, authz.ApplicationsManage); err != nil {
		return err
	}
	if err := validID(id); err != nil {
		return err
	}
	return s.base.writeAsStaff(ctx, func(ctx context.Context, q *sqlc.Queries, session staffWriteSession, epoch pgtype.UUID) error {
		if _, err := q.LockNotificationRuntime(ctx); err != nil {
			return err
		}
		app, err := lockManagedProject(ctx, q, id, session, epoch, 0)
		if err != nil {
			return err
		}
		setup, err := q.LockApplicationOnboarding(ctx, app.ID)
		if err != nil {
			return err
		}
		if app.Status == "RETIRED" {
			return ErrOnboardingConflict
		}
		return fn(ctx, q, app, session, epoch, setup)
	})
}
func (s *OnboardingService) ConfigureEnrollment(ctx context.Context, id pgtype.UUID, input onboarding.EnrollmentRequest, pre OnboardingPreconditions) (onboarding.Readiness, bool, error) {
	if _, err := s.managed(ctx, id); err != nil {
		return onboarding.Readiness{}, false, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return onboarding.Readiness{}, false, err
	}
	validated, err := onboarding.DecodeEnrollmentRequest(strings.NewReader(string(body)))
	if err != nil {
		return onboarding.Readiness{}, false, err
	}
	var approved *OnboardingProfile
	for i := range s.settings.Profiles {
		if s.settings.Profiles[i].Profile.ID == validated.ProfileID {
			approved = &s.settings.Profiles[i]
			break
		}
	}
	if approved == nil {
		return onboarding.Readiness{}, false, ErrOnboardingConflict
	}
	for _, p := range validated.Platforms {
		found := false
		for _, identifier := range approved.ApplicationIdentifiers[p.Platform] {
			if identifier == p.ApplicationIdentifier {
				found = true
				break
			}
		}
		if !found {
			return onboarding.Readiness{}, false, ErrOnboardingConflict
		}
	}
	fingerprint, err := s.fingerprint(validated)
	if err != nil {
		return onboarding.Readiness{}, false, err
	}
	var result onboarding.Readiness
	replay := false
	err = s.mutate(ctx, id, func(ctx context.Context, q *sqlc.Queries, app sqlc.Application, session staffWriteSession, epoch pgtype.UUID, setup sqlc.ApplicationOnboarding) error {
		key, err := onboardingID(pre.IdempotencyKey)
		if err != nil {
			return ErrInvalidPrecondition
		}
		old, err := q.FindApplicationOnboardingRequest(ctx, sqlc.FindApplicationOnboardingRequestParams{ApplicationID: app.ID, ActorID: session.actorID, Kind: "ENROLLMENT", IdempotencyKey: key})
		if err == nil {
			if !hmac.Equal(old.RequestFingerprint, fingerprint) || old.FingerprintKeyVersion != s.settings.FingerprintKeyVersion || !old.ExpiresAt.Time.After(time.Now()) {
				return ErrOnboardingConflict
			}
			replay = true
			return json.Unmarshal(old.ResponseData, &result)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = onboardingCondition(id, setup.Revision, pre); err != nil {
			return err
		}
		if app.Status != "PROVISIONING" || setup.CurrentConfigurationID.Valid {
			return ErrOnboardingConflict
		}
		profile, _ := onboardingID(validated.ProfileID)
		platforms, _ := json.Marshal(validated.Platforms)
		_, err = q.ConfigureApplicationEnrollment(ctx, sqlc.ConfigureApplicationEnrollmentParams{ApplicationID: app.ID, EnrollmentProfileID: profile, EnrollmentPlatforms: platforms, Revision: setup.Revision})
		if err != nil {
			return casError(err)
		}
		row, err := q.GetApplicationOnboarding(ctx, id)
		if err != nil {
			return err
		}
		result = setupView(row)
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err = q.SaveApplicationOnboardingRequest(ctx, sqlc.SaveApplicationOnboardingRequestParams{ApplicationID: app.ID, ActorID: session.actorID, Kind: "ENROLLMENT", IdempotencyKey: key, RequestFingerprint: fingerprint, FingerprintKeyVersion: s.settings.FingerprintKeyVersion, ResponseData: data}); err != nil {
			return err
		}
		return logChange(ctx, q, app, audit.ACTION_APPLICATION_ENROLLMENT_CONFIGURE, audit.ENTITY_TYPE_APPLICATION_ONBOARDING, app.ID, nil, map[string]any{"profile_id": profile.String(), "revision": result.Revision})
	})
	return result, replay, err
}

func uploadCandidateView(c sqlc.ApplicationProviderConfiguration, revision int64) onboarding.Candidate {
	return onboarding.Candidate{ID: c.ID.String(), State: onboarding.CandidateStored, TargetProjectID: c.TargetProjectID, ServiceAccountEmail: c.ServiceAccountEmail, Revision: strconv.FormatInt(revision, 10)}
}
func (s *OnboardingService) StoreFCM(ctx context.Context, id pgtype.UUID, input onboarding.FCMRequest, pre OnboardingPreconditions) (onboarding.Candidate, bool, error) {
	if _, err := s.managed(ctx, id); err != nil {
		return onboarding.Candidate{}, false, err
	}
	var identity struct {
		ClientEmail string `json:"client_email"`
	}
	if json.Unmarshal(input.ServiceAccount, &identity) != nil || fcm.ValidateServiceAccount(input.ServiceAccount, input.TargetProjectID, identity.ClientEmail) != nil {
		return onboarding.Candidate{}, false, onboarding.ErrInvalidRequest
	}
	if input.Environment != onboarding.EnvironmentStaging && input.Environment != onboarding.EnvironmentProduction {
		return onboarding.Candidate{}, false, onboarding.ErrInvalidRequest
	}
	var canonicalAccount map[string]string
	if json.Unmarshal(input.ServiceAccount, &canonicalAccount) != nil {
		return onboarding.Candidate{}, false, onboarding.ErrInvalidRequest
	}
	canonicalData, marshalErr := json.Marshal(canonicalAccount)
	input.ServiceAccount = canonicalData
	if marshalErr != nil {
		return onboarding.Candidate{}, false, onboarding.ErrInvalidRequest
	}
	if len(input.ServiceAccount) > 64*1024 {
		return onboarding.Candidate{}, false, onboarding.ErrTooLarge
	}
	fingerprint, err := s.fingerprint(input)
	if err != nil {
		return onboarding.Candidate{}, false, err
	}
	var ingestion sqlc.ApplicationOnboardingIngestion
	var result onboarding.Candidate
	replay := false
	err = s.mutate(ctx, id, func(ctx context.Context, q *sqlc.Queries, app sqlc.Application, session staffWriteSession, epoch pgtype.UUID, setup sqlc.ApplicationOnboarding) error {
		key, err := onboardingID(pre.IdempotencyKey)
		if err != nil {
			return ErrInvalidPrecondition
		}
		old, err := q.FindApplicationFCMIngestion(ctx, sqlc.FindApplicationFCMIngestionParams{ApplicationID: app.ID, ActorID: session.actorID, IdempotencyKey: key})
		if err == nil {
			if old.FingerprintKeyVersion != s.settings.FingerprintKeyVersion || !hmac.Equal(old.RequestFingerprint, fingerprint) || !old.ExpiresAt.Time.After(time.Now()) || old.State == "ABANDONED" {
				return ErrOnboardingConflict
			}
			if old.State == "RESERVED" && (old.SessionID != session.id || old.SessionEpoch != epoch || old.ExpectedRevision != setup.Revision) {
				return ErrOnboardingConflict
			}
			ingestion = old
			if old.State == "COMMITTED" {
				c, err := q.GetApplicationProviderConfiguration(ctx, sqlc.GetApplicationProviderConfigurationParams{ApplicationID: app.ID, ID: old.ConfigurationID})
				if err != nil {
					return err
				}
				result = uploadCandidateView(c, old.ExpectedRevision+1)
				replay = true
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = onboardingCondition(id, setup.Revision, pre); err != nil {
			return err
		}
		if app.Status == "HELD" || setup.Environment.Valid && setup.Environment.String != string(input.Environment) {
			return ErrOnboardingConflict
		}
		actorEpoch, err := q.GetOnboardingActorEpoch(ctx, session.actorID)
		if err != nil {
			return err
		}
		reservationID, err := newOnboardingID()
		if err != nil {
			return err
		}
		configurationID, err := newOnboardingID()
		if err != nil {
			return err
		}
		ingestion, err = q.ReserveApplicationFCMIngestion(ctx, sqlc.ReserveApplicationFCMIngestionParams{ID: reservationID, ApplicationID: app.ID, ActorID: session.actorID, SessionID: session.id, SessionEpoch: epoch, AuthEpoch: actorEpoch, ExpectedRevision: setup.Revision, IdempotencyKey: key, RequestFingerprint: fingerprint, FingerprintKeyVersion: s.settings.FingerprintKeyVersion, ConfigurationID: configurationID, Environment: string(input.Environment), TargetProjectID: input.TargetProjectID, ServiceAccountEmail: identity.ClientEmail})
		return err
	})
	if err != nil || replay {
		return result, replay, err
	}
	// External publication is deliberately outside retryable DB transactions.
	staged, err := s.settings.Stager.Stage(ctx, provisioning.StageRequest{AppID: id.String(), ReservationID: ingestion.ID.String(), ConfigID: ingestion.ConfigurationID.String(), Environment: string(input.Environment), TargetProject: input.TargetProjectID, ExpectedEmail: identity.ClientEmail, ServiceAccount: input.ServiceAccount})
	if err != nil {
		return result, false, ErrOnboardingUnavailable
	}
	if staged.Reference != ingestion.ConfigurationID.String() || staged.AppID != id.String() || staged.ConfigID != ingestion.ConfigurationID.String() || staged.Environment != string(input.Environment) || staged.Size < 1 || staged.Size > 64*1024 {
		return result, false, ErrOnboardingUnavailable
	}
	err = s.mutate(ctx, id, func(ctx context.Context, q *sqlc.Queries, app sqlc.Application, session staffWriteSession, epoch pgtype.UUID, setup sqlc.ApplicationOnboarding) error {
		current, err := q.GetApplicationFCMIngestion(ctx, sqlc.GetApplicationFCMIngestionParams{ApplicationID: app.ID, ID: ingestion.ID})
		if err != nil {
			return err
		}
		if current.ActorID != session.actorID || current.SessionID != session.id || current.SessionEpoch != epoch || !current.ExpiresAt.Time.After(time.Now()) {
			return ErrOnboardingConflict
		}
		if current.State == "COMMITTED" {
			c, err := q.GetApplicationProviderConfiguration(ctx, sqlc.GetApplicationProviderConfigurationParams{ApplicationID: app.ID, ID: current.ConfigurationID})
			if err != nil {
				return err
			}
			result = uploadCandidateView(c, current.ExpectedRevision+1)
			replay = true
			return nil
		}
		if current.State != "RESERVED" || current.ExpectedRevision != setup.Revision {
			return ErrPreconditionFailed
		}
		c, err := q.CreateApplicationProviderConfiguration(ctx, sqlc.CreateApplicationProviderConfigurationParams{ID: ingestion.ConfigurationID, ApplicationID: app.ID, Environment: string(input.Environment), TargetProjectID: input.TargetProjectID, ServiceAccountEmail: identity.ClientEmail, FileReference: ingestion.ConfigurationID, CreatedBy: session.actorID})
		if err != nil {
			return err
		}
		updated, err := q.SetApplicationOnboardingCandidate(ctx, sqlc.SetApplicationOnboardingCandidateParams{ApplicationID: app.ID, Environment: pgtype.Text{String: string(input.Environment), Valid: true}, CandidateConfigurationID: c.ID, Revision: setup.Revision})
		if err != nil {
			return casError(err)
		}
		n, err := q.CompleteApplicationFCMIngestion(ctx, sqlc.CompleteApplicationFCMIngestionParams{ApplicationID: app.ID, ID: ingestion.ID})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrOnboardingConflict
		}
		result = uploadCandidateView(c, updated.Revision)
		return logChange(ctx, q, app, audit.ACTION_APPLICATION_FCM_CONFIGURE, audit.ENTITY_TYPE_APPLICATION_ONBOARDING, app.ID, nil, map[string]any{"configuration_id": c.ID.String(), "revision": result.Revision})
	})
	return result, replay, err
}

func (s *OnboardingService) Queue(ctx context.Context, id pgtype.UUID, command OnboardingCommand, pre OnboardingPreconditions) (onboarding.Operation, bool, error) {
	if _, err := s.managed(ctx, id); err != nil {
		return onboarding.Operation{}, false, err
	}
	fingerprint, err := s.fingerprint(command)
	if err != nil {
		return onboarding.Operation{}, false, err
	}
	var result onboarding.Operation
	replay := false
	err = s.mutate(ctx, id, func(ctx context.Context, q *sqlc.Queries, app sqlc.Application, session staffWriteSession, epoch pgtype.UUID, setup sqlc.ApplicationOnboarding) error {
		key, err := onboardingID(pre.IdempotencyKey)
		if err != nil {
			return ErrInvalidPrecondition
		}
		old, err := q.FindApplicationOnboardingOperation(ctx, sqlc.FindApplicationOnboardingOperationParams{ApplicationID: app.ID, ActorID: session.actorID, Kind: string(command.Kind), IdempotencyKey: key})
		if err == nil {
			if old.FingerprintKeyVersion != s.settings.FingerprintKeyVersion || !hmac.Equal(old.RequestFingerprint, fingerprint) {
				return ErrOnboardingConflict
			}
			if old.CreatedAt.Time.Add(24 * time.Hour).Before(time.Now()) {
				return ErrOnboardingConflict
			}
			result = queuedOperationView(old)
			replay = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = onboardingCondition(id, setup.Revision, pre); err != nil {
			return err
		}
		if app.Status == "HELD" {
			return ErrOnboardingConflict
		}
		configID := setup.CurrentConfigurationID
		if command.ConfigurationID != "" {
			configID, err = onboardingID(command.ConfigurationID)
			if err != nil {
				return err
			}
		}
		config, err := q.GetApplicationProviderConfiguration(ctx, sqlc.GetApplicationProviderConfigurationParams{ApplicationID: app.ID, ID: configID})
		if err != nil {
			return appError(err)
		}
		if config.State == "SUPERSEDED" {
			return ErrOnboardingConflict
		}
		runtime, err := q.LockNotificationRuntime(ctx)
		if err != nil {
			return err
		}
		policy, err := q.GetApplicationOnboarding(ctx, id)
		if err != nil {
			return err
		}
		if policy.DispatchRevision <= 0 && command.Kind != onboarding.OperationVerify {
			return ErrOnboardingUnavailable
		}
		var approvalID, cohortID, testID pgtype.UUID
		// pgx encodes nil slices as SQL NULL; non-cohort operations store '{}'.
		installations := make([]pgtype.UUID, 0)
		switch command.Kind {
		case onboarding.OperationVerify:
			available, err := q.HasApplicationOnboardingVerifier(ctx, app.ID)
			if err != nil {
				return err
			}
			if !available {
				return ErrOnboardingUnavailable
			}
			if configID != setup.CandidateConfigurationID && configID != setup.CurrentConfigurationID {
				return ErrOnboardingConflict
			}
			n, err := q.CountRecentApplicationFCMVerifications(ctx, app.ID)
			if err != nil {
				return err
			}
			if n >= 5 {
				return ErrOnboardingRateLimited
			}
			if command.TestInstallationID != nil {
				testID, err = onboardingID(*command.TestInstallationID)
				if err != nil {
					return err
				}
				if _, err = q.FindOnboardingTestInstallation(ctx, sqlc.FindOnboardingTestInstallationParams{ApplicationID: app.ID, PublicID: testID}); err != nil {
					return appError(err)
				}
				// OAuth verification is available now; target-permission dry-run
				// requires the later approved device/cohort execution path.
				return ErrOnboardingUnavailable
			}
		case onboarding.OperationActivate:
			if app.Status != "PROVISIONING" || setup.CurrentConfigurationID.Valid || configID != setup.CandidateConfigurationID || !setup.EnrollmentProfileID.Valid || !setup.Greenfield {
				return ErrOnboardingConflict
			}
			if runtime.Sealed || runtime.MigrationHold {
				return ErrOnboardingUnavailable
			}
			checks, err := q.ListApplicationProviderChecks(ctx, sqlc.ListApplicationProviderChecksParams{ApplicationID: app.ID, ConfigurationID: configID})
			if err != nil {
				return err
			}
			passed := false
			for _, check := range checks {
				if check.Kind == "OAUTH" && check.Outcome == "PASSED" && check.ExpiresAt.Time.After(time.Now()) && check.OriginEpoch == runtime.OriginEpoch && check.SourceEpoch == runtime.SourceEpoch {
					passed = true
				}
			}
			if !passed {
				return ErrOnboardingConflict
			}
		case onboarding.OperationPromote:
			if app.Status != "ACTIVE" || !setup.CurrentConfigurationID.Valid || configID != setup.CandidateConfigurationID || configID == setup.CurrentConfigurationID {
				return ErrOnboardingConflict
			}
			checks, err := q.ListApplicationProviderChecks(ctx, sqlc.ListApplicationProviderChecksParams{ApplicationID: app.ID, ConfigurationID: configID})
			if err != nil {
				return err
			}
			passed := false
			for _, check := range checks {
				if check.Kind == "OAUTH" && check.Outcome == "PASSED" && check.ExpiresAt.Time.After(time.Now()) && check.OriginEpoch == runtime.OriginEpoch && check.SourceEpoch == runtime.SourceEpoch {
					passed = true
				}
			}
			if !passed {
				return ErrOnboardingConflict
			}
		case onboarding.OperationTestCohort, onboarding.OperationReleasePilot, onboarding.OperationReleaseFull:
			if app.Status != "ACTIVE" || configID != setup.CurrentConfigurationID {
				return ErrOnboardingConflict
			}
			approvalID, err = onboardingID(command.EvidenceID)
			if err != nil {
				return err
			}
			approval, err := q.GetApplicationOnboardingApproval(ctx, sqlc.GetApplicationOnboardingApprovalParams{ApplicationID: app.ID, ID: approvalID})
			if err != nil {
				return appError(err)
			}
			expectedKind := strings.TrimPrefix(string(command.Kind), "RELEASE_")
			if approval.Kind != expectedKind || approval.ConfigurationID != configID || approval.RevokedAt.Valid || approval.ConsumedAt.Valid || !approval.ExpiresAt.Time.After(time.Now()) || approval.OriginEpoch != runtime.OriginEpoch || approval.SourceEpoch != runtime.SourceEpoch || approval.ApplicationRevision != app.Revision || approval.DispatchRevision != policy.DispatchRevision {
				return ErrOnboardingConflict
			}
			if command.Kind == onboarding.OperationTestCohort {
				if policy.DispatchMode != "HELD" || len(command.InstallationIDs) < 1 || len(command.InstallationIDs) > 10 {
					return ErrOnboardingConflict
				}
				seen := map[pgtype.UUID]bool{}
				for _, raw := range command.InstallationIDs {
					value, err := onboardingID(raw)
					if err != nil || seen[value] {
						return ErrOnboardingConflict
					}
					seen[value] = true
					installations = append(installations, value)
				}
			} else {
				cohortID, err = onboardingID(command.CohortID)
				if err != nil {
					return err
				}
				if cohortID != setup.CohortID || cohortID != approval.CohortID {
					return ErrOnboardingConflict
				}
				if command.Kind == onboarding.OperationReleaseFull && policy.DispatchMode != "PILOT" {
					return ErrOnboardingConflict
				}
			}
		default:
			return onboarding.ErrInvalidRequest
		}
		actorEpoch, err := q.GetOnboardingActorEpoch(ctx, session.actorID)
		if err != nil {
			return err
		}
		revision, err := q.AdvanceApplicationOnboardingRevision(ctx, sqlc.AdvanceApplicationOnboardingRevisionParams{ApplicationID: app.ID, Revision: setup.Revision})
		if err != nil {
			return casError(err)
		}
		operationID, err := newOnboardingID()
		if err != nil {
			return err
		}
		operation, err := q.CreateApplicationOnboardingOperation(ctx, sqlc.CreateApplicationOnboardingOperationParams{ID: operationID, ApplicationID: app.ID, Kind: string(command.Kind), ActorID: session.actorID, SessionID: session.id, SessionEpoch: epoch, AuthEpoch: actorEpoch, IdempotencyKey: key, RequestFingerprint: fingerprint, FingerprintKeyVersion: s.settings.FingerprintKeyVersion, ConfigurationID: configID, ApprovalID: approvalID, CohortID: cohortID, TestInstallationID: testID, ExpectedSetupRevision: revision, ExpectedApplicationRevision: app.Revision, ExpectedDispatchRevision: policy.DispatchRevision, OriginEpoch: runtime.OriginEpoch, SourceEpoch: runtime.SourceEpoch, InstallationIds: installations, Reason: command.Reason})
		if err != nil {
			return onboardingDBError(err)
		}
		result = operationView(operation)
		action := audit.ACTION_APPLICATION_FCM_VERIFY
		switch command.Kind {
		case onboarding.OperationActivate:
			action = audit.ACTION_APPLICATION_ENROLLMENT_ACTIVATE
		case onboarding.OperationPromote:
			action = audit.ACTION_APPLICATION_FCM_PROMOTE
		case onboarding.OperationTestCohort:
			action = audit.ACTION_APPLICATION_TEST_COHORT_CONFIGURE
		case onboarding.OperationReleasePilot, onboarding.OperationReleaseFull:
			action = audit.ACTION_APPLICATION_DISPATCH_RELEASE
		}
		return logChange(ctx, q, app, action, audit.ENTITY_TYPE_APPLICATION_ONBOARDING, app.ID, nil, map[string]any{"operation_id": operation.ID.String(), "kind": operation.Kind})
	})
	return result, replay, err
}

func (s *OnboardingService) Hold(ctx context.Context, id pgtype.UUID, input onboarding.HoldRequest, pre OnboardingPreconditions) (onboarding.Readiness, bool, error) {
	if _, err := s.managed(ctx, id); err != nil {
		return onboarding.Readiness{}, false, err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return onboarding.Readiness{}, false, err
	}
	if _, err = onboarding.DecodeHoldRequest(strings.NewReader(string(data))); err != nil {
		return onboarding.Readiness{}, false, err
	}
	fingerprint, err := s.fingerprint(input)
	if err != nil {
		return onboarding.Readiness{}, false, err
	}
	var result onboarding.Readiness
	replay := false
	err = s.mutate(ctx, id, func(ctx context.Context, q *sqlc.Queries, app sqlc.Application, session staffWriteSession, epoch pgtype.UUID, setup sqlc.ApplicationOnboarding) error {
		key, err := onboardingID(pre.IdempotencyKey)
		if err != nil {
			return ErrInvalidPrecondition
		}
		old, err := q.FindApplicationOnboardingRequest(ctx, sqlc.FindApplicationOnboardingRequestParams{ApplicationID: app.ID, ActorID: session.actorID, Kind: "HOLD", IdempotencyKey: key})
		if err == nil {
			if old.FingerprintKeyVersion != s.settings.FingerprintKeyVersion || !hmac.Equal(old.RequestFingerprint, fingerprint) || !old.ExpiresAt.Time.After(time.Now()) {
				return ErrOnboardingConflict
			}
			replay = true
			return json.Unmarshal(old.ResponseData, &result)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = onboardingCondition(id, setup.Revision, pre); err != nil {
			return err
		}
		n, err := q.HoldApplicationOnboardingDispatch(ctx, app.ID)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrOnboardingConflict
		}
		if err = q.SupersedeApplicationOnboardingMutations(ctx, app.ID); err != nil {
			return err
		}
		if _, err = q.AdvanceApplicationOnboardingRevision(ctx, sqlc.AdvanceApplicationOnboardingRevisionParams{ApplicationID: app.ID, Revision: setup.Revision}); err != nil {
			return casError(err)
		}
		row, err := q.GetApplicationOnboarding(ctx, id)
		if err != nil {
			return err
		}
		result = setupView(row)
		payload, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err = q.SaveApplicationOnboardingRequest(ctx, sqlc.SaveApplicationOnboardingRequestParams{ApplicationID: app.ID, ActorID: session.actorID, Kind: "HOLD", IdempotencyKey: key, RequestFingerprint: fingerprint, FingerprintKeyVersion: s.settings.FingerprintKeyVersion, ResponseData: payload}); err != nil {
			return err
		}
		return logChange(ctx, q, app, audit.ACTION_APPLICATION_DISPATCH_HOLD, audit.ENTITY_TYPE_APPLICATION_ONBOARDING, app.ID, nil, map[string]any{"reason": input.Reason, "revision": result.Revision})
	})
	return result, replay, err
}

func onboardingDBError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrOnboardingConflict
	}
	return err
}

// Submission retries return the original acceptance snapshot; polling the operation
// resource separately exposes later completion and avoids stale setup ETags.
func queuedOperationView(op sqlc.ApplicationOnboardingOperation) onboarding.Operation {
	return onboarding.Operation{ID: op.ID.String(), Kind: onboarding.OperationKind(op.Kind), State: onboarding.OperationPending, CreatedAt: op.CreatedAt.Time.UTC().Format(time.RFC3339Nano)}
}

func newOnboardingID() (pgtype.UUID, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return pgtype.UUID{}, ErrOnboardingUnavailable
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}
