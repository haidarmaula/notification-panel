// Package onboarding contains the wire types and strict decoders for application onboarding.
package onboarding

import "encoding/json"

type Environment string

const (
	EnvironmentStaging    Environment = "staging"
	EnvironmentProduction Environment = "production"
)

type ApplicationStatus string

const (
	ApplicationProvisioning ApplicationStatus = "PROVISIONING"
	ApplicationActive       ApplicationStatus = "ACTIVE"
	ApplicationHeld         ApplicationStatus = "HELD"
	ApplicationRetired      ApplicationStatus = "RETIRED"
)

type DispatchMode string

const (
	DispatchHeld  DispatchMode = "HELD"
	DispatchPilot DispatchMode = "PILOT"
	DispatchFull  DispatchMode = "FULL"
)

type CheckOutcome string

const (
	CheckNotChecked   CheckOutcome = "NOT_CHECKED"
	CheckPassed       CheckOutcome = "PASSED"
	CheckFailed       CheckOutcome = "FAILED"
	CheckInconclusive CheckOutcome = "INCONCLUSIVE"
)

type OperationKind string

const (
	OperationVerify       OperationKind = "VERIFY"
	OperationActivate     OperationKind = "ACTIVATE"
	OperationPromote      OperationKind = "PROMOTE"
	OperationTestCohort   OperationKind = "TEST_COHORT"
	OperationReleasePilot OperationKind = "RELEASE_PILOT"
	OperationReleaseFull  OperationKind = "RELEASE_FULL"
)

type OperationState string

const (
	OperationPending    OperationState = "PENDING"
	OperationRunning    OperationState = "RUNNING"
	OperationSucceeded  OperationState = "SUCCEEDED"
	OperationFailed     OperationState = "FAILED"
	OperationSuperseded OperationState = "SUPERSEDED"
)

type Platform string

const (
	PlatformAndroid Platform = "ANDROID"
	PlatformIOS     Platform = "IOS"
)

type CandidateState string

const (
	CandidateStored     CandidateState = "STORED"
	CandidateVerifying  CandidateState = "VERIFYING"
	CandidateVerified   CandidateState = "VERIFIED"
	CandidateRejected   CandidateState = "REJECTED"
	CandidateSuperseded CandidateState = "SUPERSEDED"
)

type ApprovalKind string

const (
	ApprovalTestCohort ApprovalKind = "TEST_COHORT"
	ApprovalPilot      ApprovalKind = "PILOT"
	ApprovalFull       ApprovalKind = "FULL"
)

type ApprovalStatus string

const (
	ApprovalValid   ApprovalStatus = "VALID"
	ApprovalExpired ApprovalStatus = "EXPIRED"
	ApprovalRevoked ApprovalStatus = "REVOKED"
)

type ReleaseMode string

const (
	ReleasePilot ReleaseMode = "PILOT"
	ReleaseFull  ReleaseMode = "FULL"
)

type Readiness struct {
	ApplicationID            string            `json:"application_id"`
	ApplicationStatus        ApplicationStatus `json:"application_status"`
	DispatchMode             DispatchMode      `json:"dispatch_mode"`
	Revision                 string            `json:"revision"`
	Environment              *Environment      `json:"environment"`
	CurrentConfigurationID   *string           `json:"current_configuration_id"`
	CandidateConfigurationID *string           `json:"candidate_configuration_id"`
	EnrollmentProfileID      *string           `json:"enrollment_profile_id"`
	CohortID                 *string           `json:"cohort_id"`
	ChecksConfigurationID    *string           `json:"checks_configuration_id"`
	Checks                   []ReadinessCheck  `json:"checks"`
	Blockers                 []string          `json:"blockers"`
	NextActions              []string          `json:"next_actions"`
	LastOperation            *Operation        `json:"last_operation"`
}

type ReadinessCheck struct {
	Code      string       `json:"code"`
	Outcome   CheckOutcome `json:"outcome"`
	CheckedAt *string      `json:"checked_at"`
	ExpiresAt *string      `json:"expires_at"`
}

type Operation struct {
	ID                string         `json:"id"`
	Kind              OperationKind  `json:"kind"`
	State             OperationState `json:"state"`
	CreatedAt         string         `json:"created_at"`
	CompletedAt       *string        `json:"completed_at"`
	FailureCode       *string        `json:"failure_code"`
	ResultingRevision *string        `json:"resulting_revision"`
}

type Profile struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Platforms []Platform `json:"platforms"`
}

type Candidate struct {
	ID                  string         `json:"id"`
	State               CandidateState `json:"state"`
	TargetProjectID     string         `json:"target_project_id"`
	ServiceAccountEmail string         `json:"service_account_email"`
	Revision            string         `json:"revision"`
}

type Approval struct {
	ID        string         `json:"id"`
	Kind      ApprovalKind   `json:"kind"`
	Status    ApprovalStatus `json:"status"`
	ExpiresAt string         `json:"expires_at"`
}

// FCMRequest is a write-only request. ServiceAccount is never part of a response DTO.
type FCMRequest struct {
	Environment     Environment     `json:"environment"`
	TargetProjectID string          `json:"target_project_id"`
	ServiceAccount  json.RawMessage `json:"service_account"`
}

type EnrollmentRequest struct {
	ProfileID string               `json:"profile_id"`
	Platforms []EnrollmentPlatform `json:"platforms"`
}

type EnrollmentPlatform struct {
	Platform              Platform `json:"platform"`
	ApplicationIdentifier string   `json:"application_identifier"`
}

type VerifyRequest struct {
	ConfigurationID    string  `json:"configuration_id"`
	TestInstallationID *string `json:"test_installation_id,omitempty"`
}

type ConfigurationRequest struct {
	ConfigurationID string `json:"configuration_id"`
}

type TestCohortRequest struct {
	InstallationIDs []string `json:"installation_ids"`
	Reason          string   `json:"reason"`
	EvidenceID      string   `json:"evidence_id"`
}

type ReleaseRequest struct {
	Mode       ReleaseMode `json:"mode"`
	CohortID   string      `json:"cohort_id"`
	EvidenceID string      `json:"evidence_id"`
}

type HoldRequest struct {
	Reason string `json:"reason"`
}
