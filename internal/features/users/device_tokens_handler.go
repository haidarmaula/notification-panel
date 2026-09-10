package users

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"hello/pkg/response"
)

// DeviceTokenHandler handles HTTP requests for device token management.
//
// It is the HTTP adapter for the device-token CRUD surface: listing a
// user's tokens, registering a new one, updating a token's metadata, and
// deleting a token. It shares the UserService with UserHandler; the two
// handlers are wired into the same module and apply the same middleware
// chain.
//
// # Relationship to the mobile feature's sync endpoint
//
// The Register method on this handler overlaps in function with the
// mobile feature's POST /api/v1/mobile/sync endpoint. Both end up
// creating or refreshing a device_tokens row for a user. The two differ
// in authentication model:
//
//   - The mobile feature's sync endpoint authenticates the caller with a
//     Laravel-issued JWT and derives the user ID from the token's
//     claims. The caller cannot register a device for a user other than
//     themselves; the trust boundary is the JWT.
//   - This handler's Register endpoint accepts the target user ID from
//     the request body and does not authenticate the caller beyond the
//     shared API key. The trust boundary is the API key.
//
// The Register endpoint is therefore intended for first-party
// server-to-server integrations — a trusted backend that knows the user
// ID and is authorised to register a device on the user's behalf. It
// must not be exposed to untrusted clients, because any caller able to
// present the API key can register a device for any user.
//
// # Error mapping
//
// Each method recognises a small set of service sentinels
// (ErrUserNotFound, ErrDeviceTokenNotFound, ErrDeviceTokenDuplicate,
// ErrInvalidPlatform) and maps them to status codes. Any unrecognised
// error falls through to a 500 whose response body carries err.Error()
// verbatim — the same disclosure pattern used elsewhere in this
// codebase.
//
// # Concurrency
//
// A DeviceTokenHandler holds only a pointer to its service and is safe
// for concurrent use. net/http invokes Handle methods from a distinct
// goroutine per connection; the handler shares no mutable state across
// those invocations.
type DeviceTokenHandler struct {
	service *UserService
}

// NewDeviceTokenHandler creates a new DeviceTokenHandler instance.
//
// service must be non-nil; it is stored directly and dereferenced on the
// first request, so a nil value will panic at request time rather than at
// construction. In practice the service is wired by the users module's
// constructor from the shared dependencies.
func NewDeviceTokenHandler(service *UserService) *DeviceTokenHandler {
	return &DeviceTokenHandler{service: service}
}

// ListByUser handles GET /api/v1/users/{id}/device-tokens.
//
// # Path parameters
//
// The user ID is taken from the path's id parameter. It must parse as
// int64; a malformed value produces a 400 before the service is called.
//
// # Response mapping
//
// Each domain DeviceToken is converted to a DeviceTokenResponse. The
// LastSeenAt field is the one transformation beyond a field-by-field
// copy: the domain field is a *time.Time, and the handler formats it to
// RFC3339 and stores the result as a *string. When the domain value is
// nil, the DTO field is nil and the JSON key is omitted. When it is
// non-nil, the DTO field is a non-nil pointer to the formatted string.
//
// # No pagination
//
// The endpoint does not paginate. A user's device tokens are expected to
// be a small, bounded set — typically 1 to 3 — so returning them all in
// one call is safe. A pathological user with hundreds of tokens would
// produce a large response; no bound is enforced.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or malformed.
//   - 404 Not Found: the service returned ErrUserNotFound — the user
//     does not exist, checked before the token list is fetched.
//   - 500 Internal Server Error: any other service error, served with
//     err.Error() as the message.
//   - 200 OK: a ListDeviceTokensResponse containing the user's tokens.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *DeviceTokenHandler) ListByUser(w http.ResponseWriter, r *http.Request) {
	userID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid user id")
		return
	}

	tokens, err := h.service.ListDeviceTokens(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	data := make([]DeviceTokenResponse, len(tokens))
	for i, t := range tokens {
		var lastSeen *string
		if t.LastSeenAt != nil {
			val := t.LastSeenAt.Format(time.RFC3339)
			lastSeen = &val
		}
		data[i] = DeviceTokenResponse{
			ID:             t.ID,
			Platform:       t.Platform,
			InstallationID: t.InstallationID,
			IsActive:       t.IsActive,
			LastSeenAt:     lastSeen,
			CreatedAt:      t.CreatedAt.Format(time.RFC3339),
		}
	}

	response.JSON(w, http.StatusOK, ListDeviceTokensResponse{Data: data}, "success")
}

// Register handles POST /api/v1/device-tokens (for mobile app).
//
// # Request shape
//
// The body is decoded into an anonymous struct that embeds
// RegisterDeviceTokenRequest and adds a top-level UserID field. The
// effect is that the JSON body is flat — user_id, platform, push_token,
// installation_id, etc. all appear at the top level — rather than
// nesting the device fields under a device_token key as the mobile
// feature's sync request does. This is a wire-format divergence between
// the two registration endpoints; a client targeting one cannot
// trivially reuse its request builder for the other.
//
// # Trust model
//
// The endpoint authenticates the caller only with the shared API key; it
// does not inspect a JWT and does not verify that the caller is
// authorised to register a device on behalf of the supplied user_id. Any
// caller able to present the API key can register a device for any user.
//
// This is materially weaker than the mobile feature's sync endpoint,
// which derives the user identity from a verified JWT. The Register
// endpoint is intended for trusted first-party callers, and the API-key
// gate is the only thing standing between an attacker who obtains the
// key and the ability to attach arbitrary devices to arbitrary accounts.
// The endpoint should not be exposed to client-side or third-party
// integrations.
//
// # Validation
//
// Three presence checks are performed before the service is called:
// user_id must be non-zero, push_token must be non-empty, and platform
// must be non-empty. Deeper validation — that the user exists, that the
// platform is a recognised value, that the push token is not already
// registered — is the service's responsibility.
//
// # Response shape divergence from ListByUser
//
// The response is a DeviceTokenResponse, but it is populated with a
// subset of the fields that ListByUser produces: LastSeenAt is not set
// (a freshly registered token has no last-seen time yet), and UpdatedAt
// is not part of the DTO at all. A client that consumes both endpoints
// and relies on field presence should not assume the two shapes are
// identical; they share the DTO type but not every field is populated by
// every endpoint.
//
// # Responses
//
//   - 400 Bad Request: the body could not be decoded, user_id was
//     zero, push_token was empty, platform was empty, or the service
//     returned ErrInvalidPlatform.
//   - 404 Not Found: the service returned ErrUserNotFound — the
//     supplied user_id does not resolve to a row.
//   - 409 Conflict: the service returned ErrDeviceTokenDuplicate — a
//     device with the same push token is already registered. The check
//     is advisory; see the service's RegisterDeviceToken for the
//     concurrency caveat.
//   - 500 Internal Server Error: any other service error, served with
//     err.Error() as the message.
//   - 201 Created: a DeviceTokenResponse for the newly created row.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *DeviceTokenHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID int64 `json:"user_id"`
		RegisterDeviceTokenRequest
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}
	if req.UserID == 0 {
		response.JSON(w, http.StatusBadRequest, nil, "user_id is required")
		return
	}
	if req.PushToken == "" {
		response.JSON(w, http.StatusBadRequest, nil, "push_token is required")
		return
	}
	if req.Platform == "" {
		response.JSON(w, http.StatusBadRequest, nil, "platform is required")
		return
	}

	token, err := h.service.RegisterDeviceToken(r.Context(), req.UserID, req.RegisterDeviceTokenRequest)
	if err != nil {
		switch {
		case errors.Is(err, ErrUserNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrDeviceTokenDuplicate):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrInvalidPlatform):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	resp := DeviceTokenResponse{
		ID:             token.ID,
		Platform:       token.Platform,
		InstallationID: token.InstallationID,
		IsActive:       token.IsActive,
		CreatedAt:      token.CreatedAt.Format(time.RFC3339),
	}
	response.JSON(w, http.StatusCreated, resp, "device token registered")
}

// Update handles PATCH /api/v1/device-tokens/{id}.
//
// # Path parameters
//
// The token ID is taken from the path's id parameter. It must parse as
// int64; a malformed value produces a 400.
//
// # Validation
//
// After decoding, the handler rejects a request where every field of the
// UpdateDeviceTokenRequest is nil. Because every field is a pointer, the
// check is exhaustive and precise: a client that supplies only one field
// passes, and a client that supplies none is rejected without the service
// being called.
//
// # Response shape
//
// The success response is a small map containing a "message" key, not
// the updated DeviceTokenResponse. A client that needs the updated state
// must issue a follow-up GET — the endpoint does not echo it. This
// matches the segments feature's Update response shape; the staff
// feature's Update, by contrast, does echo the updated record.
//
// # Responses
//
//   - 400 Bad Request: a malformed id, a JSON decode failure, a body
//     with every field nil, or the service returned ErrInvalidPlatform.
//   - 404 Not Found: the service returned ErrDeviceTokenNotFound.
//   - 409 Conflict: the service returned ErrDeviceTokenDuplicate — the
//     new push_token collides with another token's. The check is
//     advisory; see the service's UpdateDeviceToken.
//   - 500 Internal Server Error: any other service error, served with
//     err.Error() as the message.
//   - 200 OK: a JSON object with a "message" field.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *DeviceTokenHandler) Update(w http.ResponseWriter, r *http.Request) {
	tokenID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid token id")
		return
	}

	var req UpdateDeviceTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.Platform == nil && req.PushToken == nil && req.InstallationID == nil &&
		req.AppVersion == nil && req.OSVersion == nil && req.DeviceModel == nil &&
		req.IsActive == nil {
		response.JSON(w, http.StatusBadRequest, nil, "at least one field required")
		return
	}

	err = h.service.UpdateDeviceToken(r.Context(), tokenID, req)
	if err != nil {
		switch {
		case errors.Is(err, ErrDeviceTokenNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrDeviceTokenDuplicate):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrInvalidPlatform):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{"message": "device token updated"}, "success")
}

// Delete handles DELETE /api/v1/device-tokens/{id}.
//
// # Path parameters
//
// The token ID is taken from the path's id parameter. It must parse as
// int64; a malformed value produces a 400.
//
// # No ownership check
//
// The endpoint deletes any device token identified by the path,
// regardless of the caller's relationship to the user the token belongs
// to. It does not verify that the caller is the token's owner or has
// administrative authority over the target user. The API-key gate is the
// only authorisation step.
//
// For the mobile-facing unregistration flow, this is acceptable because
// the mobile client possesses the token ID it wants to remove. For a
// server-to-server integration, the caller is expected to have already
// verified its authority before making the request.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or malformed.
//   - 404 Not Found: the service returned ErrDeviceTokenNotFound.
//   - 500 Internal Server Error: any other service error, served with
//     err.Error() as the message.
//   - 200 OK: no data payload; the message "deleted" accompanies a nil
//     data field.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent Deletes on the same ID will
// both pass the initial lookup and both attempt the delete; the outcome
// depends on the delete query's behaviour for a non-existent row.
func (h *DeviceTokenHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tokenID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid token id")
		return
	}

	err = h.service.DeleteDeviceToken(r.Context(), tokenID)
	if err != nil {
		if errors.Is(err, ErrDeviceTokenNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, nil, "deleted")
}
