package users

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"hello/internal/middleware"
	"hello/pkg/response"
)

// DeviceTokenHandler handles HTTP requests for device token management.
//
// It is the HTTP adapter for the device-token CRUD surface: listing a
// user's tokens, registering a new one, updating a token's metadata, and
// deleting a token. It shares the UserService with UserHandler, but the
// direct device-token routes use a mobile JWT middleware chain while the
// administrative user routes use staff authentication.
//
// # Relationship to the mobile feature's sync endpoint
//
// The Register, Update, and Delete methods overlap with the mobile
// feature's POST /api/v1/mobile/sync endpoint. Direct device-token routes
// require a Laravel-issued bearer JWT; their authenticated external_id is
// resolved to the local user and is used for every ownership decision.
// Request bodies and paths never supply the authenticated user identity.
//
// # Error mapping
//
// Each method recognises a small set of service sentinels
// (ErrUserNotFound, ErrDeviceTokenNotFound, ErrDeviceTokenForbidden,
// ErrDeviceTokenDuplicate, ErrInvalidPlatform) and maps them to status codes. Any unrecognised
// error falls through to a 500 whose response body is the generic string
// "internal server error". The underlying error is not disclosed. This is
// the same disclosure policy used elsewhere in this codebase.
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
// Query params: page, limit.
//
// # Path parameters
//
// The user ID is taken from the path's id parameter. It must parse as
// int64; a malformed value produces a 400 before the service is called.
//
// # Pagination
//
// The endpoint is paginated. page and limit are parsed by
// getPaginationParams, so the default page size is 10 and the ceiling is
// 100. The response carries pagination metadata under a "pagination"
// key; a caller that receives fewer tokens than the applied limit can
// determine whether more pages exist by comparing Total against
// Page*Limit.
//
// This replaces the previous implementation, which returned all tokens
// up to a hard-coded cap of 100 with no pagination metadata and no
// truncation indicator. See finding H18 in code-review.md. In practice a
// user's device-token set is small — typically one to three — so the
// default page will usually contain the full set; the pagination exists
// for the pathological case rather than for the common one.
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
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or malformed.
//   - 404 Not Found: the service returned ErrUserNotFound — the user
//     does not exist, checked before the token list is fetched.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 200 OK: a ListDeviceTokensResponse containing the requested page
//     of the user's tokens and the pagination metadata.
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

	page, limit := getPaginationParams(r)

	tokens, total, err := h.service.ListDeviceTokens(r.Context(), userID, page, limit)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
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

	resp := ListDeviceTokensResponse{
		Data: data,
		Pagination: Pagination{
			Page:  page,
			Limit: limit,
			Total: total,
		},
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// Register handles POST /api/v1/device-tokens (for mobile app).
//
// # Request shape
//
// The JSON body is RegisterDeviceTokenRequest. It contains device metadata
// only; user_id is deliberately absent because ownership comes from the
// verified mobile JWT rather than client-controlled input.
//
// # Trust model
//
// MobileJWTMiddleware verifies the Laravel JWT before this handler runs and
// puts its external_id in context. The handler resolves that identity through
// the service, so a caller cannot register a device for another user.
//
// # Validation
//
// Two presence checks are performed before the service is called:
// push_token must be non-empty and platform must be non-empty. Deeper
// validation — that the authenticated user exists, that the
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
//   - 400 Bad Request: the body could not be decoded, push_token was empty,
//     platform was empty, or the service returned ErrInvalidPlatform.
//   - 404 Not Found: the service returned ErrUserNotFound — the
//     authenticated user does not resolve to a local row.
//   - 409 Conflict: the service returned ErrDeviceTokenDuplicate — a
//     device with the same push token is already registered. The check
//     is advisory; see the service's RegisterDeviceToken for the
//     concurrency caveat.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 201 Created: a DeviceTokenResponse for the newly created row.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *DeviceTokenHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterDeviceTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
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

	externalID, ok := middleware.GetMobileExternalID(r.Context())
	if !ok {
		response.JSON(w, http.StatusUnauthorized, nil, "invalid mobile token")
		return
	}

	token, err := h.service.RegisterDeviceTokenForExternalID(r.Context(), externalID, req)
	if err != nil {
		switch {
		case errors.Is(err, ErrUserNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrDeviceTokenDuplicate):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrInvalidPlatform):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
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
// The authenticated external_id is supplied by MobileJWTMiddleware. The
// service rejects an update when the token belongs to another user.
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
//   - 403 Forbidden: the token belongs to another user.
//   - 409 Conflict: the service returned ErrDeviceTokenDuplicate — the
//     new push_token collides with another token's. The check is
//     advisory; see the service's UpdateDeviceToken.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
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

	externalID, ok := middleware.GetMobileExternalID(r.Context())
	if !ok {
		response.JSON(w, http.StatusUnauthorized, nil, "invalid mobile token")
		return
	}

	err = h.service.UpdateDeviceTokenForExternalID(r.Context(), externalID, tokenID, req)
	if err != nil {
		switch {
		case errors.Is(err, ErrDeviceTokenForbidden):
			response.JSON(w, http.StatusForbidden, nil, "forbidden")
		case errors.Is(err, ErrDeviceTokenNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrDeviceTokenDuplicate):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrInvalidPlatform):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
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
// # Ownership
//
// The handler derives the caller from the verified mobile JWT and deletes the
// token only when it belongs to that user. A token owned by another user
// produces 403 Forbidden without revealing the owner.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or malformed.
//   - 403 Forbidden: the token belongs to another user.
//   - 404 Not Found: the service returned ErrDeviceTokenNotFound.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 200 OK: no data payload; the message "deleted" accompanies a nil
//     data field.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *DeviceTokenHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tokenID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid token id")
		return
	}

	externalID, ok := middleware.GetMobileExternalID(r.Context())
	if !ok {
		response.JSON(w, http.StatusUnauthorized, nil, "invalid mobile token")
		return
	}

	err = h.service.DeleteDeviceTokenForExternalID(r.Context(), externalID, tokenID)
	if err != nil {
		if errors.Is(err, ErrDeviceTokenForbidden) {
			response.JSON(w, http.StatusForbidden, nil, "forbidden")
			return
		}
		if errors.Is(err, ErrDeviceTokenNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		return
	}

	response.JSON(w, http.StatusOK, nil, "deleted")
}
