package applications

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/onboarding"
)

const onboardingTestID = "11111111-1111-4111-8111-111111111111"
const onboardingTestOpID = "22222222-2222-4222-8222-222222222222"

type onboardingBackendFixture struct {
	calls       int
	err         error
	readiness   onboarding.Readiness
	candidate   onboarding.Candidate
	operation   onboarding.Operation
	replayed    bool
	lastCommand OnboardingCommand
	lastPre     OnboardingPreconditions
}

func (f *onboardingBackendFixture) Readiness(context.Context, pgtype.UUID) (onboarding.Readiness, error) {
	f.calls++
	return f.readiness, f.err
}
func (f *onboardingBackendFixture) Profiles(context.Context, pgtype.UUID) ([]onboarding.Profile, error) {
	f.calls++
	return nil, f.err
}
func (f *onboardingBackendFixture) Approvals(context.Context, pgtype.UUID) ([]onboarding.Approval, error) {
	f.calls++
	return nil, f.err
}
func (f *onboardingBackendFixture) Operation(context.Context, pgtype.UUID, pgtype.UUID) (onboarding.Operation, error) {
	f.calls++
	return f.operation, f.err
}
func (f *onboardingBackendFixture) ConfigureEnrollment(_ context.Context, _ pgtype.UUID, _ onboarding.EnrollmentRequest, pre OnboardingPreconditions) (onboarding.Readiness, bool, error) {
	f.calls++
	f.lastPre = pre
	return f.readiness, f.replayed, f.err
}
func (f *onboardingBackendFixture) StoreFCM(_ context.Context, _ pgtype.UUID, _ onboarding.FCMRequest, pre OnboardingPreconditions) (onboarding.Candidate, bool, error) {
	f.calls++
	f.lastPre = pre
	return f.candidate, f.replayed, f.err
}
func (f *onboardingBackendFixture) Queue(_ context.Context, _ pgtype.UUID, command OnboardingCommand, pre OnboardingPreconditions) (onboarding.Operation, bool, error) {
	f.calls++
	f.lastCommand = command
	f.lastPre = pre
	return f.operation, f.replayed, f.err
}
func (f *onboardingBackendFixture) Hold(_ context.Context, _ pgtype.UUID, _ onboarding.HoldRequest, pre OnboardingPreconditions) (onboarding.Readiness, bool, error) {
	f.calls++
	f.lastPre = pre
	return f.readiness, f.replayed, f.err
}

func onboardingRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.SetPathValue("app_id", onboardingTestID)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("If-Match", `"onboarding:`+onboardingTestID+`:7"`)
	r.Header.Set("Idempotency-Key", onboardingTestOpID)
	return r
}

func TestOnboardingPreflightRejectsInvalidInputBeforeBackend(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*http.Request)
		call   func(*OnboardingHandler, http.ResponseWriter, *http.Request)
		status int
		body   string
	}{
		{
			name: "missing If-Match", status: http.StatusPreconditionRequired,
			setup: func(r *http.Request) { r.Header.Del("If-Match") },
			call:  func(h *OnboardingHandler, w http.ResponseWriter, r *http.Request) { h.Hold(w, r) }, body: `{"reason":"hold"}`,
		},
		{
			name: "malformed If-Match", status: http.StatusBadRequest,
			setup: func(r *http.Request) { r.Header.Set("If-Match", `W/"weak"`) },
			call:  func(h *OnboardingHandler, w http.ResponseWriter, r *http.Request) { h.Hold(w, r) }, body: `{"reason":"hold"}`,
		},
		{
			name: "duplicate If-Match", status: http.StatusBadRequest,
			setup: func(r *http.Request) { r.Header.Add("If-Match", r.Header.Get("If-Match")) },
			call:  func(h *OnboardingHandler, w http.ResponseWriter, r *http.Request) { h.Hold(w, r) }, body: `{"reason":"hold"}`,
		},
		{
			name: "invalid idempotency key", status: http.StatusBadRequest,
			setup: func(r *http.Request) { r.Header.Set("Idempotency-Key", "not-a-uuid") },
			call:  func(h *OnboardingHandler, w http.ResponseWriter, r *http.Request) { h.Hold(w, r) }, body: `{"reason":"hold"}`,
		},
		{
			name: "unsupported media type", status: http.StatusUnsupportedMediaType,
			setup: func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
			call:  func(h *OnboardingHandler, w http.ResponseWriter, r *http.Request) { h.Release(w, r) }, body: `{}`,
		},
		{
			name: "unknown body field is redacted", status: http.StatusBadRequest,
			setup: func(r *http.Request) {},
			call:  func(h *OnboardingHandler, w http.ResponseWriter, r *http.Request) { h.Hold(w, r) }, body: `{"reason":"safe","private_key":"secret-marker"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &onboardingBackendFixture{}
			r := onboardingRequest(http.MethodPost, "/api/v1/admin/apps/"+onboardingTestID+"/onboarding", test.body)
			test.setup(r)
			w := httptest.NewRecorder()
			test.call(NewOnboardingHandler(backend), w, r)
			if w.Code != test.status || backend.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, backend.calls, w.Body)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q", w.Header().Get("Cache-Control"))
			}
			if strings.Contains(w.Body.String(), "secret-marker") {
				t.Fatalf("response exposed private input: %s", w.Body)
			}
		})
	}
}

func TestOnboardingOversizeIs413BeforeBackend(t *testing.T) {
	backend := &onboardingBackendFixture{}
	handler := NewOnboardingHandler(backend)
	r := onboardingRequest(http.MethodPost, "/api/v1/admin/apps/"+onboardingTestID+"/onboarding", strings.Repeat(" ", 64*1024+1))
	w := httptest.NewRecorder()
	handler.Hold(w, r)
	if w.Code != http.StatusRequestEntityTooLarge || backend.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", w.Code, backend.calls, w.Body)
	}
}

func TestOnboardingQueuedOperationHeaders(t *testing.T) {
	backend := &onboardingBackendFixture{
		operation: onboarding.Operation{ID: onboardingTestOpID, Kind: onboarding.OperationReleasePilot, State: onboarding.OperationPending},
		replayed:  true,
	}
	r := onboardingRequest(http.MethodPost, "/api/v1/admin/apps/"+onboardingTestID+"/onboarding/dispatch/release", `{"mode":"PILOT","cohort_id":"`+onboardingTestID+`","evidence_id":"`+onboardingTestOpID+`"}`)
	w := httptest.NewRecorder()
	NewOnboardingHandler(backend).Release(w, r)
	if w.Code != http.StatusAccepted || backend.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", w.Code, backend.calls, w.Body)
	}
	wantLocation := onboardingPath + onboardingTestID + "/onboarding/operations/" + onboardingTestOpID
	if w.Header().Get("Location") != wantLocation || w.Header().Get("Idempotency-Replayed") != "true" || w.Header().Get("ETag") != "" {
		t.Fatalf("unexpected operation headers: %#v", w.Header())
	}
	if backend.lastCommand.Kind != onboarding.OperationReleasePilot || backend.lastPre.IdempotencyKey != onboardingTestOpID {
		t.Fatalf("backend input = command %#v, preconditions %#v", backend.lastCommand, backend.lastPre)
	}
}

func TestOnboardingResponseErrorsAreSafeAndUncached(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		header string
	}{
		{name: "backend failure", err: errors.New("database password: private-marker"), status: http.StatusInternalServerError},
		{name: "unavailable", err: ErrOnboardingUnavailable, status: http.StatusServiceUnavailable, header: "5"},
		{name: "conflict", err: ErrOnboardingConflict, status: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &onboardingBackendFixture{err: test.err}
			r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/apps/"+onboardingTestID+"/onboarding", nil)
			r.SetPathValue("app_id", onboardingTestID)
			w := httptest.NewRecorder()
			NewOnboardingHandler(backend).Readiness(w, r)
			if w.Code != test.status || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private-marker") {
				t.Fatalf("status=%d headers=%v body=%s", w.Code, w.Header(), w.Body)
			}
			if test.header != "" && w.Header().Get("Retry-After") != test.header {
				t.Fatalf("Retry-After = %q", w.Header().Get("Retry-After"))
			}
		})
	}
}

func TestOnboardingReadinessETagAndEmptyListShape(t *testing.T) {
	backend := &onboardingBackendFixture{readiness: onboarding.Readiness{Revision: "12"}}
	handler := NewOnboardingHandler(backend)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/apps/"+onboardingTestID+"/onboarding", nil)
	r.SetPathValue("app_id", onboardingTestID)
	w := httptest.NewRecorder()
	handler.Readiness(w, r)
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"onboarding:`+onboardingTestID+`:12"` {
		t.Fatalf("status=%d ETag=%q body=%s", w.Code, w.Header().Get("ETag"), w.Body)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/admin/apps/"+onboardingTestID+"/onboarding/enrollment-profiles", nil)
	r.SetPathValue("app_id", onboardingTestID)
	w = httptest.NewRecorder()
	handler.Profiles(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
}

func TestOnboardingHandlerRequiresBackend(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewOnboardingHandler(nil) did not panic")
		}
	}()
	NewOnboardingHandler(nil)
}

func TestOnboardingPathUUIDRejectsZeroAndQueries(t *testing.T) {
	backend := &onboardingBackendFixture{}
	for _, url := range []string{"/api/v1/admin/apps/00000000-0000-0000-0000-000000000000/onboarding", "/api/v1/admin/apps/" + onboardingTestID + "/onboarding?unexpected=1"} {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		r.SetPathValue("app_id", onboardingTestID)
		if strings.Contains(url, "00000000") {
			r.SetPathValue("app_id", "00000000-0000-0000-0000-000000000000")
		}
		w := httptest.NewRecorder()
		NewOnboardingHandler(backend).Readiness(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("url %q status=%d body=%s", url, w.Code, w.Body)
		}
	}
	if backend.calls != 0 {
		t.Fatalf("invalid path/query called backend %d times", backend.calls)
	}
}

func TestOnboardingDomainValidationErrorsUseClientStatuses(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{onboarding.ErrInvalidRequest, http.StatusBadRequest},
		{onboarding.ErrTooLarge, http.StatusRequestEntityTooLarge},
		{ErrOnboardingRateLimited, http.StatusTooManyRequests},
	} {
		w := httptest.NewRecorder()
		beginOnboardingResponse(w)
		writeOnboardingError(w, tc.err)
		if w.Code != tc.status {
			t.Fatalf("wrong validation status: got %d want %d", w.Code, tc.status)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("validation response was cacheable")
		}
	}
}
