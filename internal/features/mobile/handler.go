package mobile

import (
	"encoding/json"
	"errors"
	"net/http"

	"hello/pkg/response"
)

// MobileHandler handles HTTP requests for mobile app integration.
//
// It is a thin adapter over MobileService: it parses and validates the JSON
// request body, delegates the actual sync to the service, and maps the
// service's domain errors to HTTP status codes and the standard response
// envelope. No business logic and no persistence calls live here.
//
// # Error mapping
//
// The handler recognises two sentinels returned by the service:
// ErrInvalidJWT is mapped to 401, ErrInvalidPlatform to 400. Any other error
// falls through to a 500 with the generic message "internal server error"
// rather than disclosing the underlying error. This matches the auth
// feature's handler and avoids leaking internal details to the mobile
// client.
//
// # Concurrency
//
// A MobileHandler holds only a pointer to its service and is safe for
// concurrent use. net/http invokes Handle methods from a distinct goroutine
// per connection; the handler shares no mutable state across those
// invocations.
type MobileHandler struct {
	service *MobileService
}

// NewMobileHandler creates a new MobileHandler instance.
//
// service must be non-nil; it is stored directly and dereferenced on the
// first request, so a nil value will panic at request time rather than at
// construction. In practice the service is wired by the mobile module's
// constructor, which builds the underlying repositories and provider from
// the shared dependencies.
func NewMobileHandler(service *MobileService) *MobileHandler {
	return &MobileHandler{service: service}
}

// Sync handles POST /api/v1/mobile/sync.
//
// # Request
//
// The body must be a JSON object matching SyncRequest. As with the auth
// endpoints, there is no explicit body-size limit and no Content-Type check
// — a large body consumes memory up to whatever the surrounding server
// permits, and a body sent with a non-JSON Content-Type is still decoded.
// Deployments that expose this endpoint to untrusted mobile clients should
// add a body-size limit in front of the handler.
//
// # Validation
//
// Before calling the service, the handler performs three presence checks on
// the decoded request: the JWT, the device push_token, and the platform
// must each be non-empty. A missing value is rejected with a 400 and a
// message naming the missing field. This validation is deliberately at the
// HTTP boundary rather than in the service, so that the service can assume
// well-formed input and so that malformed requests do not incur the cost of
// a JWT parse attempt.
//
// # Responses
//
//   - 400 Bad Request: the request body could not be decoded as JSON, or a
//     required field was empty.
//   - 401 Unauthorized: the service returned ErrInvalidJWT — the token was
//     missing, malformed, expired, or signed with a different secret than
//     the one configured for the mobile audience.
//   - 400 Bad Request: the service returned ErrInvalidPlatform — the
//     platform string was not one the service recognises.
//   - 500 Internal Server Error: any other service error. The response body
//     is the generic string "internal server error"; the underlying error
//     is not disclosed.
//   - 200 OK: a SyncResponse carrying the internal user and device-token
//     IDs, wrapped in the standard response envelope with the message
//     "user synchronized".
//
// # Error disclosure policy
//
// The default branch of the error switch sends the generic
// "internal server error" message rather than err.Error(). Any error that
// is not ErrInvalidJWT or ErrInvalidPlatform — a database connectivity
// failure, a unique-constraint violation with a driver-supplied message, a
// future internal sentinel — is therefore not surfaced to the mobile
// client. This is consistent with the auth feature's handler and closes the
// information-disclosure gap previously present here.
//
// # Concurrency
//
// Safe for concurrent use. Each invocation operates on its own request and
// response writer.
func (h *MobileHandler) Sync(w http.ResponseWriter, r *http.Request) {
	var req SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.JWT == "" {
		response.JSON(w, http.StatusBadRequest, nil, "jwt is required")
		return
	}
	if req.DeviceToken.PushToken == "" {
		response.JSON(w, http.StatusBadRequest, nil, "push_token is required")
		return
	}
	if req.DeviceToken.Platform == "" {
		response.JSON(w, http.StatusBadRequest, nil, "platform is required")
		return
	}

	result, err := h.service.Sync(r.Context(), SyncParams{
		JWT:            req.JWT,
		Platform:       req.DeviceToken.Platform,
		PushToken:      req.DeviceToken.PushToken,
		InstallationID: req.DeviceToken.InstallationID,
		AppVersion:     req.DeviceToken.AppVersion,
		OSVersion:      req.DeviceToken.OSVersion,
		DeviceModel:    req.DeviceToken.DeviceModel,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidJWT):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		case errors.Is(err, ErrInvalidPlatform):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	resp := SyncResponse{
		UserID:        result.UserID,
		DeviceTokenID: result.DeviceTokenID,
	}
	response.JSON(w, http.StatusOK, resp, "user synchronized")
}
