package applications

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/onboarding"
	"hello/pkg/response"
)

const onboardingPath = "/api/v1/admin/apps/"

var onboardingRevisionPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

// OnboardingBackend owns onboarding reads and mutations. Its implementation must enforce
// current authorization, revision matching, and idempotency atomically with the mutation.
type OnboardingBackend interface {
	Readiness(context.Context, pgtype.UUID) (onboarding.Readiness, error)
	Profiles(context.Context, pgtype.UUID) ([]onboarding.Profile, error)
	Approvals(context.Context, pgtype.UUID) ([]onboarding.Approval, error)
	Operation(context.Context, pgtype.UUID, pgtype.UUID) (onboarding.Operation, error)
	ConfigureEnrollment(context.Context, pgtype.UUID, onboarding.EnrollmentRequest, OnboardingPreconditions) (onboarding.Readiness, bool, error)
	StoreFCM(context.Context, pgtype.UUID, onboarding.FCMRequest, OnboardingPreconditions) (onboarding.Candidate, bool, error)
	Queue(context.Context, pgtype.UUID, OnboardingCommand, OnboardingPreconditions) (onboarding.Operation, bool, error)
	Hold(context.Context, pgtype.UUID, onboarding.HoldRequest, OnboardingPreconditions) (onboarding.Readiness, bool, error)
}

// OnboardingPreconditions carries the client concurrency token and mutation replay key.
type OnboardingPreconditions struct {
	IfMatch        string
	IdempotencyKey string
}

// OnboardingCommand describes one asynchronous onboarding operation.
type OnboardingCommand struct {
	Kind               onboarding.OperationKind
	ConfigurationID    string
	TestInstallationID *string
	CohortID           string
	EvidenceID         string
	InstallationIDs    []string
	Reason             string
}

// OnboardingHandler validates HTTP input and delegates onboarding work to its required backend.
type OnboardingHandler struct{ backend OnboardingBackend }

// NewOnboardingHandler constructs a handler with the required onboarding backend.
func NewOnboardingHandler(backend OnboardingBackend) *OnboardingHandler {
	if backend == nil {
		panic("applications: onboarding backend is required")
	}
	return &OnboardingHandler{backend: backend}
}

// Readiness returns the application's onboarding status and strong setup ETag.
func (h *OnboardingHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	beginOnboardingResponse(w)
	id, ok := onboardingApplicationID(w, r)
	if !ok || !onboardingNoQuery(w, r) {
		return
	}
	readiness, err := h.backend.Readiness(r.Context(), id)
	if err != nil {
		writeOnboardingError(w, err)
		return
	}
	if !setOnboardingETag(w, id, readiness.Revision) {
		writeOnboardingInternalError(w)
		return
	}
	response.JSON(w, http.StatusOK, readiness, "ok")
}

// Profiles lists the enrollment profiles available to the application.
func (h *OnboardingHandler) Profiles(w http.ResponseWriter, r *http.Request) {
	beginOnboardingResponse(w)
	id, ok := onboardingApplicationID(w, r)
	if !ok || !onboardingNoQuery(w, r) {
		return
	}
	items, err := h.backend.Profiles(r.Context(), id)
	if err != nil {
		writeOnboardingError(w, err)
		return
	}
	if items == nil {
		items = []onboarding.Profile{}
	}
	response.JSON(w, http.StatusOK, struct {
		Items []onboarding.Profile `json:"items"`
	}{Items: items}, "ok")
}

// Approvals lists the application's onboarding approvals.
func (h *OnboardingHandler) Approvals(w http.ResponseWriter, r *http.Request) {
	beginOnboardingResponse(w)
	id, ok := onboardingApplicationID(w, r)
	if !ok || !onboardingNoQuery(w, r) {
		return
	}
	items, err := h.backend.Approvals(r.Context(), id)
	if err != nil {
		writeOnboardingError(w, err)
		return
	}
	if items == nil {
		items = []onboarding.Approval{}
	}
	response.JSON(w, http.StatusOK, struct {
		Items []onboarding.Approval `json:"items"`
	}{Items: items}, "ok")
}

// Operation returns an onboarding operation by its public ID.
func (h *OnboardingHandler) Operation(w http.ResponseWriter, r *http.Request) {
	beginOnboardingResponse(w)
	id, ok := onboardingApplicationID(w, r)
	if !ok || !onboardingNoQuery(w, r) {
		return
	}
	operationID, ok := onboardingPathUUID(w, r.PathValue("operation_id"))
	if !ok {
		return
	}
	operation, err := h.backend.Operation(r.Context(), id, operationID)
	if err != nil {
		writeOnboardingError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, operation, "ok")
}

// Enrollment configures the application's enrollment profile and platform identifiers.
func (h *OnboardingHandler) Enrollment(w http.ResponseWriter, r *http.Request) {
	beginOnboardingResponse(w)
	id, preconditions, ok := onboardingMutationInput(w, r)
	if !ok {
		return
	}
	var request onboarding.EnrollmentRequest
	if !decodeOnboardingBody(w, r, onboarding.DecodeEnrollmentRequest, &request) {
		return
	}
	readiness, replayed, err := h.backend.ConfigureEnrollment(r.Context(), id, request, preconditions)
	if err != nil {
		writeOnboardingError(w, err)
		return
	}
	if !setOnboardingETag(w, id, readiness.Revision) {
		writeOnboardingInternalError(w)
		return
	}
	w.Header().Set("Idempotency-Replayed", replayedValue(replayed))
	response.JSON(w, http.StatusOK, readiness, "enrollment configured")
}

// FCM stores an FCM credential candidate.
func (h *OnboardingHandler) FCM(w http.ResponseWriter, r *http.Request) {
	beginOnboardingResponse(w)
	id, preconditions, ok := onboardingMutationInput(w, r)
	if !ok {
		return
	}
	var request onboarding.FCMRequest
	if !decodeOnboardingBody(w, r, onboarding.DecodeFCMRequest, &request) {
		return
	}
	candidate, replayed, err := h.backend.StoreFCM(r.Context(), id, request, preconditions)
	if err != nil {
		writeOnboardingError(w, err)
		return
	}
	if !setOnboardingETag(w, id, candidate.Revision) {
		writeOnboardingInternalError(w)
		return
	}
	w.Header().Set("Idempotency-Replayed", replayedValue(replayed))
	response.JSON(w, http.StatusCreated, candidate, "FCM candidate stored")
}

// Verify queues an FCM candidate verification.
func (h *OnboardingHandler) Verify(w http.ResponseWriter, r *http.Request) {
	if !h.queueRequest(w, r, func() (OnboardingCommand, error) {
		decoded, err := onboarding.DecodeVerifyRequest(r.Body)
		return OnboardingCommand{Kind: onboarding.OperationVerify, ConfigurationID: decoded.ConfigurationID, TestInstallationID: decoded.TestInstallationID}, err
	}) {
		return
	}
}

// Activate queues enrollment activation.
func (h *OnboardingHandler) Activate(w http.ResponseWriter, r *http.Request) {
	h.queueRequest(w, r, func() (OnboardingCommand, error) {
		decoded, err := onboarding.DecodeConfigurationRequest(r.Body)
		return OnboardingCommand{Kind: onboarding.OperationActivate, ConfigurationID: decoded.ConfigurationID}, err
	})
}

// Promote queues promotion of a verified FCM candidate.
func (h *OnboardingHandler) Promote(w http.ResponseWriter, r *http.Request) {
	h.queueRequest(w, r, func() (OnboardingCommand, error) {
		decoded, err := onboarding.DecodeConfigurationRequest(r.Body)
		return OnboardingCommand{Kind: onboarding.OperationPromote, ConfigurationID: decoded.ConfigurationID}, err
	})
}

// TestCohort queues a test cohort delivery operation.
func (h *OnboardingHandler) TestCohort(w http.ResponseWriter, r *http.Request) {
	h.queueRequest(w, r, func() (OnboardingCommand, error) {
		decoded, err := onboarding.DecodeTestCohortRequest(r.Body)
		return OnboardingCommand{
			Kind: onboarding.OperationTestCohort, InstallationIDs: decoded.InstallationIDs,
			Reason: decoded.Reason, EvidenceID: decoded.EvidenceID,
		}, err
	})
}

// Release queues a pilot or full dispatch release.
func (h *OnboardingHandler) Release(w http.ResponseWriter, r *http.Request) {
	h.queueRequest(w, r, func() (OnboardingCommand, error) {
		decoded, err := onboarding.DecodeReleaseRequest(r.Body)
		kind := onboarding.OperationReleasePilot
		if decoded.Mode == onboarding.ReleaseFull {
			kind = onboarding.OperationReleaseFull
		}
		return OnboardingCommand{Kind: kind, CohortID: decoded.CohortID, EvidenceID: decoded.EvidenceID}, err
	})
}

func (h *OnboardingHandler) Hold(w http.ResponseWriter, r *http.Request) {
	beginOnboardingResponse(w)
	id, preconditions, ok := onboardingMutationInput(w, r)
	if !ok {
		return
	}
	var request onboarding.HoldRequest
	if !decodeOnboardingBody(w, r, onboarding.DecodeHoldRequest, &request) {
		return
	}
	readiness, replayed, err := h.backend.Hold(r.Context(), id, request, preconditions)
	if err != nil {
		writeOnboardingError(w, err)
		return
	}
	if !setOnboardingETag(w, id, readiness.Revision) {
		writeOnboardingInternalError(w)
		return
	}
	w.Header().Set("Idempotency-Replayed", replayedValue(replayed))
	response.JSON(w, http.StatusOK, readiness, "dispatch held")
}

type onboardingDecoder[T any] func(r io.Reader) (T, error)

func decodeOnboardingBody[T any](w http.ResponseWriter, r *http.Request, decode onboardingDecoder[T], dst *T) bool {
	if !onboardingJSONMediaType(r) {
		response.JSON(w, http.StatusUnsupportedMediaType, nil, "unsupported media type")
		return false
	}
	value, err := decode(r.Body)
	if errors.Is(err, onboarding.ErrTooLarge) {
		response.JSON(w, http.StatusRequestEntityTooLarge, nil, "request body too large")
		return false
	}
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return false
	}
	*dst = value
	return true
}

func (h *OnboardingHandler) queueRequest(w http.ResponseWriter, r *http.Request, decode func() (OnboardingCommand, error)) bool {
	beginOnboardingResponse(w)
	id, preconditions, ok := onboardingMutationInput(w, r)
	if !ok {
		return false
	}
	if !onboardingJSONMediaType(r) {
		response.JSON(w, http.StatusUnsupportedMediaType, nil, "unsupported media type")
		return false
	}
	command, err := decode()
	if errors.Is(err, onboarding.ErrTooLarge) {
		response.JSON(w, http.StatusRequestEntityTooLarge, nil, "request body too large")
		return false
	}
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return false
	}
	operation, replayed, err := h.backend.Queue(r.Context(), id, command, preconditions)
	if err != nil {
		writeOnboardingError(w, err)
		return false
	}
	if !onboardingPathUUIDValid(operation.ID) {
		writeOnboardingInternalError(w)
		return false
	}
	if operation.ResultingRevision != nil && !setOnboardingETag(w, id, *operation.ResultingRevision) {
		writeOnboardingInternalError(w)
		return false
	}
	w.Header().Set("Location", onboardingOperationLocation(id, operation.ID))
	w.Header().Set("Idempotency-Replayed", replayedValue(replayed))
	response.JSON(w, http.StatusAccepted, operation, "operation queued")
	return true
}

func onboardingMutationInput(w http.ResponseWriter, r *http.Request) (pgtype.UUID, OnboardingPreconditions, bool) {
	id, ok := onboardingApplicationID(w, r)
	if !ok || !onboardingNoQuery(w, r) {
		return pgtype.UUID{}, OnboardingPreconditions{}, false
	}
	match := r.Header.Values("If-Match")
	if len(match) == 0 {
		response.JSON(w, http.StatusPreconditionRequired, nil, "If-Match is required")
		return pgtype.UUID{}, OnboardingPreconditions{}, false
	}
	if len(match) != 1 {
		response.JSON(w, http.StatusBadRequest, nil, "invalid If-Match header")
		return pgtype.UUID{}, OnboardingPreconditions{}, false
	}
	ifMatch := strings.Trim(match[0], " \t")
	if !validOnboardingETag(ifMatch, id) {
		response.JSON(w, http.StatusBadRequest, nil, "invalid If-Match header")
		return pgtype.UUID{}, OnboardingPreconditions{}, false
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !onboardingPathUUIDValid(keys[0]) {
		response.JSON(w, http.StatusBadRequest, nil, "invalid idempotency key")
		return pgtype.UUID{}, OnboardingPreconditions{}, false
	}
	return id, OnboardingPreconditions{IfMatch: ifMatch, IdempotencyKey: keys[0]}, true
}

func onboardingApplicationID(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	return onboardingPathUUID(w, r.PathValue("app_id"))
}

func onboardingPathUUID(w http.ResponseWriter, value string) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if len(value) != 36 || id.Scan(value) != nil || !id.Valid || id.Bytes == [16]byte{} || id.String() != value {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return pgtype.UUID{}, false
	}
	return id, true
}

func onboardingPathUUIDValid(value string) bool {
	var id pgtype.UUID
	return len(value) == 36 && id.Scan(value) == nil && id.Valid && id.Bytes != [16]byte{} && id.String() == value
}

func onboardingNoQuery(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" {
		response.JSON(w, http.StatusBadRequest, nil, "invalid query parameters")
		return false
	}
	return true
}

func onboardingJSONMediaType(r *http.Request) bool {
	values := r.Header.Values("Content-Type")
	if len(values) != 1 {
		return false
	}
	media, params, err := mime.ParseMediaType(values[0])
	if err != nil || !strings.EqualFold(media, "application/json") {
		return false
	}
	for name, value := range params {
		if name != "charset" || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	encodings := r.Header.Values("Content-Encoding")
	return len(encodings) == 0 || len(encodings) == 1 && strings.EqualFold(strings.Trim(encodings[0], " \t"), "identity")
}

func validOnboardingETag(value string, id pgtype.UUID) bool {
	prefix := `"onboarding:` + id.String() + `:`
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, `"`) {
		return false
	}
	revision := strings.TrimSuffix(strings.TrimPrefix(value, prefix), `"`)
	return onboardingRevisionPattern.MatchString(revision)
}

func setOnboardingETag(w http.ResponseWriter, id pgtype.UUID, revision string) bool {
	if !onboardingRevisionPattern.MatchString(revision) {
		return false
	}
	w.Header().Set("ETag", fmt.Sprintf(`"onboarding:%s:%s"`, id.String(), revision))
	return true
}

func onboardingOperationLocation(id pgtype.UUID, operationID string) string {
	return onboardingPath + id.String() + "/onboarding/operations/" + operationID
}

func replayedValue(replayed bool) string {
	if replayed {
		return "true"
	}
	return "false"
}

func beginOnboardingResponse(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

func writeOnboardingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, onboarding.ErrTooLarge):
		response.JSON(w, http.StatusRequestEntityTooLarge, nil, "request body too large")
	case errors.Is(err, onboarding.ErrInvalidRequest):
		response.JSON(w, http.StatusBadRequest, nil, "invalid onboarding request")
	case errors.Is(err, ErrOnboardingRateLimited):
		w.Header().Set("Retry-After", "60")
		response.JSON(w, http.StatusTooManyRequests, nil, "application onboarding rate limit exceeded")
	case errors.Is(err, ErrOnboardingUnavailable):
		w.Header().Set("Retry-After", "5")
		response.JSON(w, http.StatusServiceUnavailable, nil, "onboarding temporarily unavailable")
	case errors.Is(err, ErrOnboardingConflict):
		response.JSON(w, http.StatusConflict, nil, "onboarding state conflict")
	default:
		writeError(w, err)
	}
}

func writeOnboardingInternalError(w http.ResponseWriter) {
	response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
}
