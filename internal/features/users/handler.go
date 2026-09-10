package users

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"hello/internal/middleware"
	"hello/internal/token"
	"hello/pkg/response"
)

// UserHandler handles HTTP requests for user management.
//
// It is the HTTP adapter for the user-facing read endpoints of the users
// feature: listing, getting by ID, searching, and reading a user's
// segment memberships and notification history. It also hosts the
// package's helper functions for parsing URL path parameters and
// pagination query strings, which the device-token handler shares.
//
// # Read-only surface
//
// Every method in this handler is a read. There is no Create, Update, or
// Delete for a user row — those operations are performed by the upstream
// Laravel backend and ingested via the mobile feature's sync endpoint,
// not by an administrative edit surface in this service. See the package
// documentation for the design rationale.
//
// # Error mapping
//
// Each method recognises ErrUserNotFound and maps it to 404; every other
// service error falls through to a 500 with err.Error() as the message.
// The 500 disclosure pattern is shared with the notifications, mobile,
// segments, staff, and profile features; the auth feature's handler is
// the only one that replaces the 500 message with a generic string.
//
// # Concurrency
//
// A UserHandler holds only a pointer to its service and is safe for
// concurrent use. net/http invokes Handle methods from a distinct
// goroutine per connection; the handler shares no mutable state across
// those invocations.
type UserHandler struct {
	service *UserService
}

// NewUserHandler creates a new UserHandler instance.
//
// service must be non-nil; it is stored directly and dereferenced on the
// first request, so a nil value will panic at request time rather than at
// construction. In practice the service is wired by the users module's
// constructor from the shared dependencies.
func NewUserHandler(service *UserService) *UserHandler {
	return &UserHandler{service: service}
}

// List handles GET /api/v1/users.
// Query params: page, limit, keyword, status, external_id.
//
// # Filtering
//
// All four filters are optional and independent. keyword searches across
// name and email; status filters on the user's lifecycle state;
// external_id performs an exact-match lookup on the upstream identifier.
// When more than one filter is supplied, the underlying query composes
// them with AND semantics — consult the SQL for the exact predicate, as
// the service simply passes the values through.
//
// The handler performs no filter-specific validation: an unknown status
// value produces an empty result set rather than an error, and an empty
// external_id is treated as "no filter on that column".
//
// # Pagination
//
// page and limit are parsed by getPaginationParams with the defaults and
// clamping policy documented on that helper. The response echoes the
// values that were actually applied, not the values the client sent.
//
// # Responses
//
//   - 200 OK: a ListUsersResponse with the current page of users and
//     pagination metadata.
//   - 500 Internal Server Error: any service error, served with
//     err.Error() as the message.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	page, limit := getPaginationParams(r)
	keyword := r.URL.Query().Get("keyword")
	status := r.URL.Query().Get("status")
	externalID := r.URL.Query().Get("external_id")

	users, total, err := h.service.List(r.Context(), page, limit, keyword, status, externalID)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	data := make([]UserResponse, len(users))
	for i, u := range users {
		data[i] = UserResponse{
			ID:         u.ID,
			ExternalID: u.ExternalID,
			Name:       u.Name,
			Email:      u.Email,
			Status:     u.Status,
			CreatedAt:  u.CreatedAt.Format(time.RFC3339),
			UpdatedAt:  u.UpdatedAt.Format(time.RFC3339),
		}
	}

	resp := ListUsersResponse{
		Data: data,
		Pagination: Pagination{
			Page:  page,
			Limit: limit,
			Total: total,
		},
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// GetByID handles GET /api/v1/users/{id}.
//
// # Path parameters
//
// The id must parse as int64; a missing or malformed value produces a
// 400 before the service is called.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or malformed.
//   - 404 Not Found: the service returned ErrUserNotFound.
//   - 500 Internal Server Error: any other service error, served with
//     err.Error() as the message.
//   - 200 OK: the UserResponse for the identified user.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *UserHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid user id")
		return
	}

	user, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	resp := UserResponse{
		ID:         user.ID,
		ExternalID: user.ExternalID,
		Name:       user.Name,
		Email:      user.Email,
		Status:     user.Status,
		CreatedAt:  user.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  user.UpdatedAt.Format(time.RFC3339),
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// Search handles GET /api/v1/users/search for autocomplete.
// Query param: keyword (required).
//
// # Required keyword
//
// Unlike List — whose keyword is optional — Search requires a non-empty
// keyword. A request without one is rejected with a 400 before the
// service is called. There is no upper bound on the keyword's length and
// no minimum, so a single character will produce a search.
//
// # Response shape
//
// The service returns a slice of SearchUserResult values, which the
// handler passes through unchanged. The response does not carry
// pagination metadata — Search returns a bounded set of matches, not a
// paginated view — and it does not echo the keyword that was searched.
//
// # Responses
//
//   - 400 Bad Request: the keyword query parameter was empty.
//   - 500 Internal Server Error: any service error, served with
//     err.Error() as the message.
//   - 200 OK: a slice of SearchUserResult.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *UserHandler) Search(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")
	if keyword == "" {
		response.JSON(w, http.StatusBadRequest, nil, "keyword is required")
		return
	}

	results, err := h.service.Search(r.Context(), keyword)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, results, "success")
}

// GetUserSegments handles GET /api/v1/users/{id}/segments.
//
// # Response shape
//
// The service returns a slice of UserSegment domain values, which the
// handler maps to a ListUserSegmentsResponse. There is no pagination: a
// user's segment memberships are expected to be a small set, so the
// endpoint returns them all in one call. The segment IDs and names are
// the only fields carried; a caller needing full segment details must
// follow up against the segments feature's endpoints.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or malformed.
//   - 404 Not Found: the service returned ErrUserNotFound.
//   - 500 Internal Server Error: any other service error, served with
//     err.Error() as the message.
//   - 200 OK: a ListUserSegmentsResponse.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *UserHandler) GetUserSegments(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid user id")
		return
	}

	segments, err := h.service.GetUserSegments(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	data := make([]UserSegmentResponse, len(segments))
	for i, s := range segments {
		data[i] = UserSegmentResponse{
			ID:   s.ID,
			Name: s.Name,
		}
	}
	response.JSON(w, http.StatusOK, ListUserSegmentsResponse{Data: data}, "success")
}

// GetUserNotifications handles GET /api/v1/users/{id}/notifications.
// Query params: page, limit.
//
// # Pagination
//
// Unlike the segment list on the same resource — which is unpaginated —
// the notification history is paginated, on the assumption that a user
// may accumulate many notifications over time. page and limit are parsed
// by getPaginationParams with the defaults and clamping policy documented
// on that helper.
//
// # Response mapping
//
// The domain UserNotification's OpenedAt field is a *string, and the
// handler passes it through unchanged. There is no parsing of the value
// into a time.Time at any point in this feature — the string arrives
// pre-formatted from the underlying query and is emitted as-is. This is
// documented as a representation caveat on UserNotification; it is noted
// here as well so a client that expects a time.Time-shaped value is not
// surprised.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or malformed.
//   - 404 Not Found: the service returned ErrUserNotFound.
//   - 500 Internal Server Error: any other service error, served with
//     err.Error() as the message.
//   - 200 OK: a ListUserNotificationsResponse with the current page and
//     pagination metadata.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *UserHandler) GetUserNotifications(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid user id")
		return
	}

	page, limit := getPaginationParams(r)

	notifications, total, err := h.service.GetUserNotifications(r.Context(), id, page, limit)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	data := make([]UserNotificationHistoryItem, len(notifications))
	for i, n := range notifications {
		data[i] = UserNotificationHistoryItem{
			NotificationID: n.NotificationID,
			Title:          n.Title,
			Status:         n.Status,
			OpenedAt:       n.OpenedAt,
		}
	}

	resp := ListUserNotificationsResponse{
		Data: data,
		Pagination: Pagination{
			Page:  page,
			Limit: limit,
			Total: total,
		},
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// ============================================
// Helper Functions
// ============================================

// parseInt64FromPath extracts and parses an int64 from a URL path parameter.
//
// # Behaviour
//
// The function reads the path parameter named by key via r.PathValue,
// which is populated only for routes registered with the Go 1.22
// method-and-wildcard ServeMux syntax. An empty value or a non-numeric
// string both produce a non-nil error; the caller is expected to map any
// error to a 400. The returned int64 on error is 0, but callers should
// treat the value as unspecified when the error is non-nil.
//
// The function does not range-check the parsed value; a caller-supplied
// negative number is accepted and returned as-is. It does not distinguish
// "the parameter was missing" from "the parameter was not a number" —
// both produce an error whose message is "missing id".
//
// # Duplication
//
// This is at least the fourth copy of this helper in the codebase — the
// others appear in the notifications, segments, and staff features, and
// possibly in mobile and users depending on the file layout. All are
// unexported and identical. A future refactor that extracts these into a
// shared request-parsing helper package would collapse the copies into
// one.
func parseInt64FromPath(r *http.Request, key string) (int64, error) {
	raw := r.PathValue(key)
	if raw == "" {
		return 0, errors.New("missing id")
	}
	return strconv.ParseInt(raw, 10, 64)
}

// getPaginationParams extracts page and limit from query parameters with defaults.
//
// # Defaults and clamping
//
// The function applies a default of page=1 and limit=10 when a parameter
// is absent, and silently clamps out-of-range values rather than rejecting
// them:
//
//   - A page value that fails to parse, or that parses to zero or a
//     negative number, is replaced by 1.
//   - A limit value that fails to parse, or that parses to zero, a
//     negative number, or a value greater than 100, is replaced by 10.
//
// Clients should treat the values echoed in the response's Pagination
// object as authoritative, not the values they sent.
//
// # Duplication
//
// This function is byte-for-byte identical to the versions in the
// notifications, segments, and staff features. The duplication is a
// consequence of the helpers being unexported in each package; a shared
// request-parsing helper package would collapse them.
func getPaginationParams(r *http.Request) (page, limit int32) {
	page = 1
	limit = 10
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.ParseInt(p, 10, 32); err == nil && v > 0 {
			page = int32(v)
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.ParseInt(l, 10, 32); err == nil && v > 0 && v <= 100 {
			limit = int32(v)
		}
	}
	return
}

// getStaffIDFromContext retrieves staff ID from context.
//
// It reads middleware.UserContextKey and type-asserts the value to
// *token.AccessClaims, returning the StaffID field and a boolean
// indicating whether a valid claim was found.
//
// # Dead code in this package
//
// Unlike its counterparts in the audit, notifications, profile, and
// segments packages, this copy of the extraction helper is never
// referenced by any function in the users feature. Every method in
// UserHandler and DeviceTokenHandler operates on the target user
// identified by the URL path or request body, not on the acting staff
// user; no method in this package needs to know who is making the
// request. The helper is therefore dead code — a leftover from an
// earlier design in which the users feature performed audit-log
// attribution, or a copy-paste from a sibling package.
//
// The declaration is retained here rather than removed because Rule 3 of
// this documentation pass prohibits executable changes. A future cleanup
// would remove it.
//
// # Duplication — fifth copy in the codebase
//
// Setting aside the dead-code status, the function is semantically
// identical to audit.GetStaffID, notifications.getStaffIDFromContext,
// profile.GetStaffIDFromContext, and segments.getStaffIDFromContext. All
// five read the same context key, perform the same type assertion, and
// return the same two values. Any change to how staff identity is
// represented in the context must be applied in all five places — or, if
// the users copy is removed as dead code, in the remaining four.
func getStaffIDFromContext(ctx context.Context) (int64, bool) {
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	if !ok || claims == nil {
		return 0, false
	}
	return claims.StaffID, true
}
