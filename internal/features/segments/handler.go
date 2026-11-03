package segments

import (
	"errors"
	"hello/internal/authz"
	"hello/internal/httprequest"
	"net/http"
	"strconv"

	"hello/pkg/response"
)

// SegmentHandler handles HTTP requests for segment management.
//
// It is the HTTP adapter for the segment feature: it parses URL path
// parameters and query strings, decodes and validates JSON request
// bodies, delegates business logic to SegmentService, and maps service
// errors to HTTP status codes and the standard response envelope.
//
// # Error mapping
//
// Each handler method recognises a small set of service sentinels
// (ErrSegmentNotFound, ErrSegmentNameTaken, ErrSegmentHasMembers,
// ErrStaffNotFoundOrInactive) and maps them to specific status codes.
// Any unrecognised error — including repository failures and wrapped
// errors from the service — falls through to a 500 whose response body
// is the generic string "internal server error". This is the same
// disclosure policy used by the notifications, mobile, and profile
// features; the auth feature also uses a generic message.
//
// # Concurrency
//
// A SegmentHandler holds only a pointer to its service and is safe for
// concurrent use. net/http invokes Handle methods from a distinct
// goroutine per connection; the handler shares no mutable state across
// those invocations.
type SegmentHandler struct {
	service *SegmentService
}

// NewSegmentHandler creates a new SegmentHandler instance.
//
// service must be non-nil; it is stored directly and dereferenced on the
// first request, so a nil value will panic at request time rather than at
// construction. In practice the service is wired by the segment module's
// constructor from the shared dependencies.
func NewSegmentHandler(service *SegmentService) *SegmentHandler {
	return &SegmentHandler{service: service}
}

// List handles GET /api/v1/segments.
//
// # Query parameters
//
// The endpoint accepts two optional query parameters:
//
//   - page and limit, parsed by getPaginationParams with the defaults and
//     clamping policy documented on that helper.
//   - search, an optional keyword passed through to the service. An empty
//     search string selects the unfiltered list path.
//
// # Response mapping
//
// The service returns a slice of domain Segment objects (with computed
// member counts and a joined staff reference). The handler maps each to a
// SegmentListItem, one field at a time. The Description field is set by
// taking the address of the domain object's Description string — because
// that string is never nil (a plain `string` in the domain model), the
// resulting pointer is always non-nil, even when the description is
// empty. The omitempty tag on the DTO field therefore never fires, and
// the JSON response always includes a `description` key. A client that
// relies on the key's absence to mean "no description set" will be
// disappointed; the client must inspect the value instead.
//
// # Responses
//
//   - 200 OK: a ListSegmentsResponse with the current page of segments
//     and pagination metadata.
//   - 500 Internal Server Error: any service error. The response body is
//     the generic string "internal server error"; the underlying error
//     is not disclosed.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *SegmentHandler) List(w http.ResponseWriter, r *http.Request) {
	page, limit := getPaginationParams(r)
	search := r.URL.Query().Get("search")

	segments, total, err := h.service.List(r.Context(), page, limit, search)
	if err != nil {
		if authz.WriteError(w, err) {
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		return
	}

	// Map domain to response DTOs
	data := make([]SegmentListItem, len(segments))
	for i, s := range segments {
		data[i] = SegmentListItem{
			ID:          s.ID,
			Name:        s.Name,
			Description: &s.Description,
			CreatedBy: StaffBrief{
				ID:   s.CreatedBy.ID,
				Name: s.CreatedBy.Name,
			},
			MemberCount: s.MemberCount,
			CreatedAt:   s.CreatedAt,
			UpdatedAt:   s.UpdatedAt,
		}
	}

	resp := ListSegmentsResponse{
		Data: data,
		Pagination: Pagination{
			Page:  page,
			Limit: limit,
			Total: total,
		},
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// GetByID handles GET /api/v1/segments/{id}.
//
// # Path parameters
//
// The id must be a parseable int64; a missing or malformed value is
// rejected with 400 before the service is called.
//
// # Response mapping
//
// As with List, the Description field is populated by taking the address
// of the domain object's Description string, so the DTO's
// Description pointer is always non-nil in the response. The
// omitempty tag is therefore ineffective for this field; see List for
// the client-facing consequence.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or not a valid
//     int64.
//   - 404 Not Found: the service returned ErrSegmentNotFound.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 200 OK: a SegmentDetail.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *SegmentHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid segment id")
		return
	}

	segmentWithCount, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		if authz.WriteError(w, err) {
			return
		}
		if errors.Is(err, ErrSegmentNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		return
	}

	detail := SegmentDetail{
		ID:          segmentWithCount.ID,
		Name:        segmentWithCount.Name,
		Description: &segmentWithCount.Description,
		CreatedBy: StaffBrief{
			ID:   segmentWithCount.CreatedBy.ID,
			Name: segmentWithCount.CreatedBy.Name,
		},
		MemberCount: segmentWithCount.MemberCount,
		CreatedAt:   segmentWithCount.CreatedAt,
		UpdatedAt:   segmentWithCount.UpdatedAt,
	}

	response.JSON(w, http.StatusOK, detail, "success")
}

// Create handles POST /api/v1/segments.
//
// # Validation
//
// The method decodes the body into a CreateSegmentRequest, then rejects
// an empty Name with a 400. It extracts the acting staff ID from the
// request context and rejects a request without an identity with a 401.
// Description is passed through without validation; a nil pointer means
// "no description", and an empty-string pointer means "empty description"
// — the two are distinguishable at the storage layer.
//
// # Responses
//
//   - 400 Bad Request: the body could not be decoded, or Name was empty.
//   - 401 Unauthorized: the request context carries no staff identity,
//     or the service returned ErrStaffNotFoundOrInactive.
//   - 409 Conflict: the service returned ErrSegmentNameTaken — a segment
//     with the same name already exists. The check is advisory; a
//     concurrent create with the same name can race past it and produce
//     a database-level unique-violation, which surfaces through the
//     default 500 branch instead. The default 500 message is the generic
//     "internal server error" and does not disclose the database error.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 201 Created: a CreateSegmentResponse with the new segment's ID,
//     under the message "segment created".
//
// # Concurrency
//
// Safe for concurrent use.
//
// JSON input is limited to 64 KiB; oversized bodies return 413 before service work.
func (h *SegmentHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateSegmentRequest
	if !httprequest.DecodeJSON(w, r, &req, httprequest.DefaultJSONLimit) {
		return
	}
	if req.Name == "" {
		response.JSON(w, http.StatusBadRequest, nil, "name is required")
		return
	}

	staffID, ok := getStaffIDFromContext(r.Context())
	if !ok || staffID == 0 {
		response.JSON(w, http.StatusUnauthorized, nil, "unauthorized")
		return
	}

	segment, err := h.service.Create(r.Context(), CreateParams{
		Name:        req.Name,
		Description: req.Description,
		CreatedBy:   staffID,
	})
	if err != nil {
		if authz.WriteError(w, err) {
			return
		}
		switch {
		case errors.Is(err, ErrSegmentNameTaken):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrStaffNotFoundOrInactive):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	resp := CreateSegmentResponse{ID: segment.ID}
	response.JSON(w, http.StatusCreated, resp, "segment created")
}

// Update handles PATCH /api/v1/segments/{id}.
//
// # Validation
//
// The method parses the path's id, decodes the body into an
// UpdateSegmentRequest, and rejects a request with both Name and
// Description nil. Because both fields are pointers, the update has true
// PATCH semantics: a request that supplies only Name will not clear the
// description, and vice versa.
//
// # Response shape
//
// Unlike the Create and GetByID responses, the Update response does not
// return the updated segment. It returns a small map containing a
// human-readable message and the segment's ID (as a string). A client
// that needs the updated state must issue a follow-up GET. This
// divergence from the notifications feature — which returns the same
// small-map shape — is consistent within the segments package but means
// the update path does not echo the new state.
//
// # Responses
//
//   - 400 Bad Request: a malformed id, a JSON decode failure, or a body
//     with neither Name nor Description set.
//   - 404 Not Found: the service returned ErrSegmentNotFound.
//   - 409 Conflict: the service returned ErrSegmentNameTaken — the new
//     name collides with an existing segment. As with Create, the check
//     is advisory and a concurrent update can race past it.
//   - 401 Unauthorized: the service returned ErrStaffNotFoundOrInactive.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 200 OK: the small-map response described above.
//
// # Concurrency
//
// Safe for concurrent use.
//
// JSON input is limited to 64 KiB; oversized bodies return 413 before service work.
func (h *SegmentHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid segment id")
		return
	}

	var req UpdateSegmentRequest
	if !httprequest.DecodeJSON(w, r, &req, httprequest.DefaultJSONLimit) {
		return
	}
	if req.Name == nil && req.Description == nil {
		response.JSON(w, http.StatusBadRequest, nil, "at least one field required")
		return
	}

	updated, err := h.service.Update(r.Context(), UpdateParams{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		if authz.WriteError(w, err) {
			return
		}
		switch {
		case errors.Is(err, ErrSegmentNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrSegmentNameTaken):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrStaffNotFoundOrInactive):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{
		"message": "segment updated",
		"id":      strconv.FormatInt(updated.ID, 10),
	}, "success")
}

// Delete handles DELETE /api/v1/segments/{id}.
//
// # Deletion preconditions
//
// The service rejects deletion of a segment that still has members, with
// ErrSegmentHasMembers. This is a safety check: a segment referenced by
// an in-flight notification's targets would leave the fan-out pipeline
// with an unresolvable audience if the segment were removed. The check is
// performed at the service layer, not by the schema, so it is not
// enforced against direct database access.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or malformed.
//   - 404 Not Found: the service returned ErrSegmentNotFound.
//   - 409 Conflict: the service returned ErrSegmentHasMembers — the
//     segment cannot be deleted while it has members.
//   - 401 Unauthorized: the service returned ErrStaffNotFoundOrInactive.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 200 OK: no data payload; the message "deleted" accompanies a nil
//     data field.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *SegmentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid segment id")
		return
	}

	err = h.service.Delete(r.Context(), id)
	if err != nil {
		if authz.WriteError(w, err) {
			return
		}
		switch {
		case errors.Is(err, ErrSegmentNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrSegmentHasMembers):
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
