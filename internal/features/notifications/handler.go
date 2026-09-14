package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"hello/internal/middleware"
	"hello/internal/token"
	"hello/pkg/response"
)

// contextKeyStaffID is a private type reserved for future use as a context
// key for staff identity within the notifications feature.
//
// It is currently declared but not referenced anywhere in this package. The
// handler reads the staff identity through getStaffIDFromContext, which
// uses middleware.UserContextKey — the same key the audit package uses —
// rather than this type. If a future revision needs a notifications-local
// context key distinct from the shared middleware key, this is where to
// add it; until then, the declaration is dead code and can be removed
// without behavioural effect.
type contextKeyStaffID struct{}

// NotificationHandler handles HTTP requests for notifications.
//
// It is the HTTP adapter for the notification feature: it parses URL path
// parameters and query strings, decodes and partially validates the JSON
// request bodies, delegates business logic to NotificationService, and maps
// service errors to HTTP status codes and the standard response envelope.
//
// # Validation split
//
// Presence and format checks that can be made without domain knowledge live
// here: the title and body must be non-empty on create, at least one field
// must be present on update, the target-type-specific fields must be
// present for the corresponding type, and the numeric path parameter must
// parse. Cross-row checks — does the template exist, is the notification in
// DRAFT, is the scheduled time in the future — live in the service.
//
// # Error mapping
//
// Every handler maps a set of service sentinels to specific status codes
// (404, 409, 400, 401) and falls through to a 500 with the generic message
// "internal server error" for anything unrecognised. No error details are
// disclosed to the client. As with the auth feature's handler, this avoids
// leaking database errors or wrapped repository errors. The auth feature
// uses the same generic message.
//
// # Concurrency
//
// A NotificationHandler holds only a pointer to its service and is safe for
// concurrent use. net/http invokes Handle methods from a distinct goroutine
// per connection; the handler shares no mutable state across those
// invocations.
type NotificationHandler struct {
	service *NotificationService
}

// NewNotificationHandler creates a new NotificationHandler instance.
//
// service must be non-nil; it is stored directly and dereferenced on the
// first request, so a nil value will panic at request time rather than at
// construction. In practice the service is wired by the notification
// module's constructor from the shared dependencies.
func NewNotificationHandler(service *NotificationService) *NotificationHandler {
	return &NotificationHandler{
		service: service,
	}
}

// List handles GET /api/v1/notifications.
// Query params: page, limit, status, type (target type), keyword.
//
// # Query parameters
//
// All four parameters are optional. Page and limit are parsed by
// getPaginationParams, which applies defaults and clamps out-of-range
// values; see that function for the exact rules. Status, type, and keyword
// are passed through to the service as-is. An empty string for any of them
// is treated as "no filter on that column" by the service's underlying
// query.
//
// # Responses
//
//   - 200 OK: a ListNotificationsResponse carrying the current page and the
//     total matching row count.
//   - 500 Internal Server Error: any service error, including a repository
//     failure. The response body is the generic string "internal server
//     error"; the underlying error is not disclosed.
//
// The handler does not distinguish "no matching rows" from "empty page of a
// larger result set"; the client must inspect Pagination.Total.
//
// # Concurrency
//
// Safe for concurrent use. Each invocation operates on its own request and
// response writer.
func (h *NotificationHandler) List(w http.ResponseWriter, r *http.Request) {
	page, limit := getPaginationParams(r)
	status := r.URL.Query().Get("status")
	targetType := r.URL.Query().Get("type")
	keyword := r.URL.Query().Get("keyword")

	items, total, err := h.service.List(r.Context(), page, limit, status, targetType, keyword)
	if err != nil {
		response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		return
	}

	resp := ListNotificationsResponse{
		Data: items,
		Pagination: Pagination{
			Page:  page,
			Limit: limit,
			Total: total,
		},
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// GetByID handles GET /api/v1/notifications/{id}.
//
// # Path parameters
//
// The id must be a parseable int64; a missing or malformed value is
// rejected with 400 before the service is called.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or not a valid
//     int64.
//   - 404 Not Found: the service returned ErrNotificationNotFound.
//   - 500 Internal Server Error: any other service error. The response body
//     is the generic string "internal server error"; the underlying error
//     is not disclosed.
//   - 200 OK: a NotificationDetail.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *NotificationHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid notification id")
		return
	}

	detail, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotificationNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		return
	}

	response.JSON(w, http.StatusOK, detail, "success")
}

// Create handles POST /api/v1/notifications.
//
// # Validation sequence
//
// The handler performs the following checks in order, each returning 400
// with a specific message before the next:
//
//  1. The body must decode as JSON.
//  2. Title and Body must both be non-empty.
//  3. The staff user must be present in the context (see
//     getStaffIDFromContext); missing identity yields 401.
//  4. Type defaults to "BROADCAST" if empty.
//  5. If Type is "INDIVIDUAL", UserIDs must be non-empty.
//  6. If Type is "SEGMENT", SegmentID must be non-nil.
//
// Cross-row validation (template existence, segment existence, future
// scheduling time) is performed by the service and surfaced through its
// sentinels.
//
// # Responses
//
//   - 400 Bad Request: a JSON decode failure, a missing Title or Body, a
//     missing target-type-specific field, or a service validation error
//     (ErrInvalidTargetType, ErrTemplateNotFound, ErrSegmentNotFound,
//     ErrInvalidScheduledTime, ErrTargetsRequired).
//   - 401 Unauthorized: the request context lacks a staff identity, or the
//     service reports the staff user is missing or inactive
//     (ErrStaffNotFoundOrInactive).
//   - 500 Internal Server Error: any other service error. The response body
//     is the generic string "internal server error"; the underlying error
//     is not disclosed.
//   - 201 Created: a CreateNotificationResponse.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *NotificationHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.Title == "" || req.Body == "" {
		response.JSON(w, http.StatusBadRequest, nil, "title and body required")
		return
	}

	staffID, ok := getStaffIDFromContext(r.Context())
	if !ok || staffID == 0 {
		response.JSON(w, http.StatusUnauthorized, nil, "unauthorized")
		return
	}

	if req.Type == "" {
		req.Type = "BROADCAST"
	}

	if req.Type == string(TargetIndividual) && len(req.UserIDs) == 0 {
		response.JSON(w, http.StatusBadRequest, nil, "user_ids required for INDIVIDUAL type")
		return
	}
	if req.Type == string(TargetSegment) && req.SegmentID == nil {
		response.JSON(w, http.StatusBadRequest, nil, "segment_id required for SEGMENT type")
		return
	}

	result, err := h.service.Create(r.Context(), CreateParams{
		Title:       req.Title,
		Body:        req.Body,
		TemplateID:  req.TemplateID,
		TargetType:  req.Type,
		SegmentID:   req.SegmentID,
		UserIDs:     req.UserIDs,
		ScheduledAt: req.ScheduledAt,
		CreatedBy:   staffID,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidTargetType), errors.Is(err, ErrTemplateNotFound),
			errors.Is(err, ErrSegmentNotFound), errors.Is(err, ErrInvalidScheduledTime),
			errors.Is(err, ErrTargetsRequired):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		case errors.Is(err, ErrStaffNotFoundOrInactive):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	response.JSON(w, http.StatusCreated, result, "created")
}

// Update handles PATCH /api/v1/notifications/{id}.
//
// # Validation sequence
//
//  1. The path's id must parse as int64.
//  2. The body must decode as JSON.
//  3. At least one of Title, Body, TemplateID, or ScheduledAt must be
//     non-nil. An entirely empty update body is rejected with 400.
//
// # Responses
//
//   - 400 Bad Request: a malformed id, a JSON decode failure, an empty
//     update body, or a service validation error (ErrTemplateNotFound,
//     ErrInvalidScheduledTime).
//   - 404 Not Found: the service returned ErrNotificationNotFound.
//   - 409 Conflict: the service returned ErrNotificationNotDraft — the
//     notification has moved past DRAFT and cannot be edited.
//   - 401 Unauthorized: the service returned ErrStaffNotFoundOrInactive.
//   - 500 Internal Server Error: any other service error. The response body
//     is the generic string "internal server error"; the underlying error
//     is not disclosed.
//   - 200 OK: a JSON object with a "message" field. This response does not
//     echo the updated notification — the client must re-fetch to obtain
//     the current state.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *NotificationHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid notification id")
		return
	}

	var req UpdateNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.Title == nil && req.Body == nil && req.TemplateID == nil && req.ScheduledAt == nil {
		response.JSON(w, http.StatusBadRequest, nil, "at least one field required")
		return
	}

	err = h.service.Update(r.Context(), id, UpdateParams{
		Title:       req.Title,
		Body:        req.Body,
		TemplateID:  req.TemplateID,
		ScheduledAt: req.ScheduledAt,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotificationNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrNotificationNotDraft):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrTemplateNotFound), errors.Is(err, ErrInvalidScheduledTime):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		case errors.Is(err, ErrStaffNotFoundOrInactive):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{"message": "notification updated"}, "success")
}

// Delete handles DELETE /api/v1/notifications/{id}.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or malformed.
//   - 404 Not Found: the service returned ErrNotificationNotFound.
//   - 409 Conflict: the service returned ErrCannotDeleteSent — the
//     notification has been sent and cannot be deleted. Only notifications
//     in DRAFT (and, depending on the service's rules, SCHEDULED) are
//     eligible for deletion.
//   - 401 Unauthorized: the service returned ErrStaffNotFoundOrInactive.
//   - 500 Internal Server Error: any other service error. The response body
//     is the generic string "internal server error"; the underlying error
//     is not disclosed.
//   - 200 OK: no payload; the "deleted" message accompanies a nil data
//     field.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *NotificationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid notification id")
		return
	}

	err = h.service.Delete(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotificationNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrCannotDeleteSent):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrStaffNotFoundOrInactive):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	response.JSON(w, http.StatusOK, nil, "deleted")
}

// Send handles POST /api/v1/notifications/{id}/send.
// It publishes a send requested event to Kafka.
//
// # Flow
//
// The handler parses the id, calls the service's Send method, and — on
// success — returns 202 Accepted. The 202 status is deliberate: the send
// endpoint does not wait for delivery to complete, only for the notification
// to be queued. Actual dispatch happens asynchronously via the Kafka
// pipeline, and the client is expected to poll the detail endpoint to
// observe the notification's status transitions from QUEUED through
// COMPLETED.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or malformed.
//   - 404 Not Found: the service returned ErrNotificationNotFound.
//   - 409 Conflict: the service returned ErrNotificationNotDraft — the
//     notification is not in DRAFT, so it cannot be sent. Notably this
//     includes a notification that has already been sent: the caller must
//     check status before retrying.
//   - 401 Unauthorized: the service returned ErrStaffNotFoundOrInactive.
//   - 500 Internal Server Error: any other service error. The response body
//     is the generic string "internal server error"; the underlying error
//     is not disclosed.
//   - 202 Accepted: a JSON object with a "message" field confirming that
//     the notification was atomically claimed as QUEUED and its send event
//     was accepted by the Kafka producer.
//
// # Kafka publish failure
//
// The service claims DRAFT to QUEUED before publishing. A publication failure
// is served as 500 and triggers a best-effort conditional rollback to DRAFT.
// Kafka and PostgreSQL do not share a transaction, so a crash or an ambiguous
// broker write still requires an outbox-based design for absolute durability.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *NotificationHandler) Send(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid notification id")
		return
	}

	err = h.service.Send(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotificationNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrNotificationNotDraft):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrStaffNotFoundOrInactive):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	response.JSON(w, http.StatusAccepted, map[string]string{"message": "notification queued for sending"}, "success")
}

// parseInt64FromPath extracts and parses int64 from URL path parameter.
//
// # Parameters
//
// r is the incoming request; key is the name of the path parameter as
// declared in the ServeMux pattern — for example, "id" for a route
// registered as "GET /api/v1/notifications/{id}". The function reads the
// value via r.PathValue, which is only populated for patterns registered
// with the Go 1.22 method-and-wildcard syntax.
//
// # Behaviour
//
// An empty path value (unlikely for a matched route) or a non-numeric
// string both produce a non-nil error; the caller is expected to map any
// error to a 400. The returned int64 on error is 0, but callers should
// treat the value as unspecified when the error is non-nil.
//
// The function does not range-check the parsed value; a caller-supplied
// negative number is accepted and returned as-is.
func parseInt64FromPath(r *http.Request, key string) (int64, error) {
	raw := r.PathValue(key)
	if raw == "" {
		return 0, errors.New("missing id")
	}
	return strconv.ParseInt(raw, 10, 64)
}

// getPaginationParams extracts page and limit from query parameters.
//
// # Defaults and clamping
//
// The function applies a default of page=1 and limit=10 when a parameter is
// absent. It also silently clamps out-of-range values rather than rejecting
// them:
//
//   - A page value that fails to parse, or that parses to zero or a
//     negative number, is replaced by 1. The client receives the default
//     page without any indication that the requested page was invalid.
//   - A limit value that fails to parse, or that parses to zero, a
//     negative number, or a value greater than 100, is replaced by 10. A
//     client requesting limit=5000 therefore receives a page of 10 items
//     with no indication that its requested limit was rejected.
//
// This silent-clamping behaviour is a deliberate API design choice — the
// alternative would be a 400 for out-of-range values — but it means the
// values echoed back in the Pagination response are not necessarily the
// values the client sent. Clients should treat the response's Limit field
// as authoritative.
//
// # Not validated here
//
// The function does not validate the interaction between page and limit;
// an out-of-range page (for example, page 100 when only 3 pages exist)
// yields an empty Data slice with a non-zero Total, which the client must
// interpret.
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
// indicating whether a valid claim was found. An empty claims value, a
// wrong type at the key, or a missing key all yield (0, false); the
// handler treats that result as an unauthenticated request and returns
// 401.
//
// # Duplication with the audit package
//
// This function is semantically identical to audit.GetStaffID: both read
// the same context key, perform the same type assertion, and return the
// same two values. The two exist because the notifications package cannot
// import the audit package without creating a cycle — the audit package
// imports middleware, and the notifications package imports both. A
// future refactor that moves the extraction helper into a shared
// dependency-free location would eliminate the duplication; the comment
// here documents the state so that a change to the context representation
// is applied in both places.
//
// # Staff ID semantics
//
// The returned ID is the internal staff_users.id, not any external
// identifier. A returned value of 0 with ok == true is theoretically
// possible (a token with a StaffID of 0) but is treated as unauthenticated
// by the callers, which check both ok and staffID != 0.
func getStaffIDFromContext(ctx context.Context) (int64, bool) {
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	if !ok || claims == nil {
		return 0, false
	}
	return claims.StaffID, true
}
