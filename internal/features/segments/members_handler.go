package segments

import (
	"errors"
	"hello/internal/authz"
	"hello/internal/httprequest"
	"net/http"

	"hello/pkg/response"
)

// MembersHandler handles HTTP requests for segment members.
//
// It is the HTTP adapter for the members sub-resource: it parses the
// segment ID and (for removal) the user ID from the URL path, decodes the
// JSON body for additions, delegates to MembersService, and maps the
// service's domain errors to status codes and the standard response
// envelope.
//
// # Error mapping
//
// The three methods recognise a small set of service sentinels
// (ErrSegmentNotFound, ErrUserNotFound, ErrMemberAlreadyExists,
// ErrMemberNotFound) and map them to status codes. Any unrecognised error
// falls through to a safe generic 500. Authorization errors are mapped to
// 401/403 before the existing domain branches.
//
// Note that, for the reasons documented on MembersService.RemoveMember,
// ErrMemberNotFound is never actually produced by the service, so the
// corresponding 404 branch in RemoveMember's switch is unreachable.
//
// # Concurrency
//
// A MembersHandler holds only a pointer to its service and is safe for
// concurrent use. net/http invokes Handle methods from a distinct
// goroutine per connection; the handler shares no mutable state across
// those invocations.
type MembersHandler struct {
	service *MembersService
}

// NewMembersHandler creates a new MembersHandler instance.
//
// service must be non-nil; it is stored directly and dereferenced on the
// first request, so a nil value will panic at request time rather than at
// construction. In practice the service is wired by the segments module's
// constructor from the shared dependencies.
func NewMembersHandler(service *MembersService) *MembersHandler {
	return &MembersHandler{service: service}
}

// ListMembers handles GET /api/v1/segments/{id}/members.
//
// # Path and query parameters
//
// The segment ID is taken from the URL path's id parameter. Pagination is
// controlled by the page and limit query parameters, parsed by
// getPaginationParams with the defaults and clamping policy documented on
// that helper.
//
// # Response mapping
//
// Each domain Member is converted to a MemberListItem, with one
// transformation beyond the field-by-field copy: the CreatedAt timestamp
// is formatted as a string using the layout "2006-01-02T15:04:05Z".
//
// That layout is not RFC3339 despite its resemblance. In Go's time
// formatting, the trailing "Z" in that string is a *literal* Z, not a
// timezone indicator; the Z07:00 form is required for timezone-aware
// formatting. The output therefore always ends with the character "Z",
// regardless of whether the underlying time is in UTC. For the current
// schema — where created_at is a timestamptz column that pgx returns in
// UTC — this happens to be correct, but it would silently misrepresent a
// non-UTC time as UTC if the schema or driver ever changed. The profile
// package, by contrast, uses time.RFC3339 for the same purpose.
//
// The conversion is done here rather than in the service or model because
// the underlying CreatedAt is a time.Time and the DTO field is a string;
// the handler is the layer that owns the wire representation.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or malformed.
//   - 404 Not Found: the service returned ErrSegmentNotFound — the
//     segment does not exist.
//   - 500 Internal Server Error: any other service error, served with
//     the literal internal server error as the message.
//   - 200 OK: a ListMembersResponse with the current page of members and
//     pagination metadata.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *MembersHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
	segmentID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid segment id")
		return
	}

	page, limit := getPaginationParams(r)

	members, total, err := h.service.ListMembers(r.Context(), segmentID, page, limit)
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

	data := make([]MemberListItem, len(members))
	for i, m := range members {
		data[i] = MemberListItem{
			ID:        m.ID,
			UserID:    m.UserID,
			Name:      m.Name,
			Email:     m.Email,
			CreatedAt: m.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	resp := ListMembersResponse{
		Data: data,
		Pagination: Pagination{
			Page:  page,
			Limit: limit,
			Total: total,
		},
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// AddMember handles POST /api/v1/segments/{id}/members.
//
// # Validation
//
// The segment ID is parsed from the path, the request body is decoded as
// an AddMemberRequest, and a zero UserID is rejected with a 400. The
// service then verifies that both the segment and the user exist before
// attempting to create the membership.
//
// # The duplicate-add path is advisory
//
// Despite the handler mapping ErrMemberAlreadyExists to 409, the service's
// existence check is defective (see MembersService.AddMember for the
// details). A duplicate add against a segment with more than one member
// typically passes the check and reaches the insert, where it either
// produces a unique-violation error (served as 500) or creates a
// duplicate membership row. The 409 branch is therefore reachable only in
// the narrow case where the target user happens to be the first-listed
// member of the segment.
//
// # Responses
//
//   - 400 Bad Request: a malformed segment ID, a JSON decode failure, or
//     a zero user_id.
//   - 404 Not Found: the service returned ErrSegmentNotFound or
//     ErrUserNotFound.
//   - 409 Conflict: the service returned ErrMemberAlreadyExists. See the
//     caveat above on when this is actually reachable.
//   - 500 Internal Server Error: any other service error, served with
//     the literal internal server error as the message.
//   - 201 Created: no data payload; the message "member added"
//     accompanies a nil data field.
//
// # Concurrency
//
// Safe for concurrent use.
//
// JSON input is limited to 64 KiB; oversized bodies return 413 before service work.
func (h *MembersHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	segmentID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid segment id")
		return
	}

	var req AddMemberRequest
	if !httprequest.DecodeJSON(w, r, &req, httprequest.DefaultJSONLimit) {
		return
	}
	if req.UserID == 0 {
		response.JSON(w, http.StatusBadRequest, nil, "user_id is required")
		return
	}

	err = h.service.AddMember(r.Context(), segmentID, req.UserID)
	if err != nil {
		if authz.WriteError(w, err) {
			return
		}
		switch {
		case errors.Is(err, ErrSegmentNotFound), errors.Is(err, ErrUserNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrMemberAlreadyExists):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	response.JSON(w, http.StatusCreated, nil, "member added")
}

// RemoveMember handles DELETE /api/v1/segments/{id}/members/{userId}.
//
// # Path parameters
//
// Two path parameters are read: the segment ID under the key "id" and the
// user ID under the key "userId". Both must parse as int64; a malformed
// value for either produces a 400.
//
// # Error mapping
//
// The error switch contains two branches that both produce a 404
// response: the first recognises ErrSegmentNotFound and ErrUserNotFound,
// the second recognises ErrMemberNotFound. Since the service's
// RemoveMember never returns ErrMemberNotFound (see its documentation for
// the analysis), the second branch is unreachable and its 404 is
// redundant with the first. It is documented here rather than removed
// because the sentinel it names may become reachable if the service is
// corrected.
//
// # Responses
//
//   - 400 Bad Request: the segment ID or the user ID is missing or
//     malformed.
//   - 404 Not Found: the service returned ErrSegmentNotFound or
//     ErrUserNotFound. Also (in the current defective implementation) the
//     unreachable ErrMemberNotFound branch.
//   - 500 Internal Server Error: any other service error, served with
//     the literal internal server error as the message.
//   - 200 OK: no data payload; the message "member removed" accompanies
//     a nil data field.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *MembersHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	segmentID, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid segment id")
		return
	}

	userID, err := parseInt64FromPath(r, "userId")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid user id")
		return
	}

	err = h.service.RemoveMember(r.Context(), segmentID, userID)
	if err != nil {
		if authz.WriteError(w, err) {
			return
		}
		switch {
		case errors.Is(err, ErrSegmentNotFound), errors.Is(err, ErrUserNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrMemberNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	response.JSON(w, http.StatusOK, nil, "member removed")
}
