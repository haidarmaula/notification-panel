package provisioning

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"hello/internal/jsonobject"
)

const maxStageRequestSize = 96 * 1024

var errServiceConfig = errors.New("invalid provisioning service configuration")

// StageRequest is the only accepted ingestion payload. It contains no path
// fields; the service derives the protected destination from canonical IDs.
type StageRequest struct {
	AppID          string          `json:"application_id"`
	ReservationID  string          `json:"reservation_id"`
	ConfigID       string          `json:"configuration_id"`
	Environment    string          `json:"environment"`
	TargetProject  string          `json:"target_project_id"`
	ExpectedEmail  string          `json:"service_account_email"`
	ServiceAccount json.RawMessage `json:"service_account"`
}

// ReservationVerifier confirms that this stage was reserved for the supplied
// application and exact credential identity before the store writes bytes.
type ReservationVerifier interface {
	VerifyStage(ctx context.Context, appID, reservationID, configID, environment, targetProject, expectedEmail string) error
}

// CredentialStore stages validated credential bytes under its fixed root.
type CredentialStore interface {
	Stage(ctx context.Context, appID, environment, configID, targetProject, expectedEmail string, data []byte) (StageResult, error)
}

// Service exposes the single authenticated POST /v1/stages operation.
type Service struct {
	identity      string
	environment   string
	targetProject string
	verifier      ReservationVerifier
	store         CredentialStore
}

// NewService requires the exact URI identity expected on the ingress client
// certificate, a reservation verifier, and a credential store.
func NewService(identity string, verifier ReservationVerifier, store CredentialStore) (*Service, error) {
	u, err := url.Parse(identity)
	if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.String() != identity ||
		strings.ContainsAny(identity, "\r\n\x00") || verifier == nil || store == nil {
		return nil, errServiceConfig
	}
	return &Service{identity: identity, verifier: verifier, store: store}, nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s == nil || r == nil || r.URL.Path != "/v1/stages" || r.Method != http.MethodPost {
		writeServiceError(w, http.StatusNotFound, "not found")
		return
	}
	if !authenticatedIdentity(r.TLS, s.identity) {
		writeServiceError(w, http.StatusForbidden, "forbidden")
		return
	}
	mediaType, parameters, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" ||
		(parameters["charset"] != "" && !strings.EqualFold(parameters["charset"], "utf-8")) {
		writeServiceError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if r.ContentLength > maxStageRequestSize {
		writeServiceError(w, http.StatusRequestEntityTooLarge, "request too large")
		return
	}
	if r.Body == nil {
		writeServiceError(w, http.StatusBadRequest, "invalid request")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxStageRequestSize+1))
	if err != nil {
		writeServiceError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len(data) > maxStageRequestSize {
		writeServiceError(w, http.StatusRequestEntityTooLarge, "request too large")
		return
	}
	if !jsonobject.UniqueLowercaseFields(data) {
		writeServiceError(w, http.StatusBadRequest, "invalid request")
		return
	}
	request := StageRequest{}
	if jsonobject.Decode(data, map[string]any{
		"application_id": &request.AppID, "reservation_id": &request.ReservationID,
		"configuration_id": &request.ConfigID, "environment": &request.Environment,
		"target_project_id": &request.TargetProject, "service_account_email": &request.ExpectedEmail,
		"service_account": &request.ServiceAccount,
	}) != nil || !canonicalUUID(request.AppID) || !canonicalUUID(request.ReservationID) || !canonicalUUID(request.ConfigID) ||
		(request.Environment != "staging" && request.Environment != "production") || len(request.ServiceAccount) == 0 || len(request.ServiceAccount) > maxCredentialSize {
		writeServiceError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if (s.environment != "" && request.Environment != s.environment) ||
		(s.targetProject != "" && request.TargetProject != s.targetProject) {
		writeServiceError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err := s.verifier.VerifyStage(r.Context(), request.AppID, request.ReservationID, request.ConfigID,
		request.Environment, request.TargetProject, request.ExpectedEmail); err != nil {
		writeServiceError(w, http.StatusForbidden, "forbidden")
		return
	}
	result, err := s.store.Stage(r.Context(), request.AppID, request.Environment, request.ConfigID,
		request.TargetProject, request.ExpectedEmail, request.ServiceAccount)
	if err != nil || result.AppID != request.AppID || result.ConfigID != request.ConfigID ||
		result.Environment != request.Environment || result.Reference != request.ConfigID ||
		result.Size != int64(len(request.ServiceAccount)) || result.Size < 1 || result.Size > maxCredentialSize {
		writeServiceError(w, http.StatusServiceUnavailable, "staging unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(result)
}

func authenticatedIdentity(state *tls.ConnectionState, identity string) bool {
	if state == nil || len(state.VerifiedChains) == 0 {
		return false
	}
	for _, chain := range state.VerifiedChains {
		if len(chain) == 0 || chain[0] == nil {
			continue
		}
		for _, uri := range chain[0].URIs {
			if uri != nil && uri.String() == identity {
				return true
			}
		}
	}
	return false
}

func writeServiceError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: message})
}
