package staff

import (
	"context"
	"encoding/json"
	"errors"
	"hello/internal/authz"
	"net/http"
	"strconv"
	"time"

	"hello/pkg/response"
)

// StaffServicer is the service contract the StaffHandler depends on.
//
// It is declared here rather than in interfaces.go, and it is unusual in
// two respects. First, it is the only handler-side interface in the
// codebase: every other feature module wires its handler to a concrete
// *Service type. Second, its method set is exactly the public API of the
// staff service, so the interface is a full abstraction rather than a
// narrow slice of what the handler needs.
//
// # Rationale
//
// The interface exists for testability. A test can substitute a fake
// implementation of StaffServicer without constructing the real service,
// its repositories, or their dependencies. The trade-off is that the
// interface must be kept in sync with the concrete service: adding a
// method to *StaffService does not automatically expose it to the
// handler, and a signature mismatch between an interface method and its
// concrete implementation surfaces as a compile error at the wiring site
// rather than here.
//
// # Context passing
//
// Every method takes a context.Context as its first parameter. This is
// the request context from the HTTP layer, and it carries the acting
// staff user's identity (via middleware.UserContextKey). The service
// checks current database authority on every operation and derives the
// self-deletion principal and audit attribution from the authenticated identity.
type StaffServicer interface {
	// Create provisions a new staff account. See StaffService.Create for
	// the validation and error semantics.
	Create(ctx context.Context, params CreateStaffParams) (*Staff, error)

	// GetByID loads a single staff user by ID. See StaffService.GetByID.
	GetByID(ctx context.Context, id int64) (*Staff, error)

	// List returns a page of staff users, optionally filtered by a search
	// keyword. See StaffService.List.
	List(ctx context.Context, page, limit int32, search string) ([]Staff, int64, error)

	// Update applies a metadata update to an existing staff user. See
	// StaffService.Update.
	Update(ctx context.Context, params UpdateStaffParams) (*Staff, error)

	// UpdateStatus activates or deactivates a staff account. See
	// StaffService.UpdateStatus.
	UpdateStatus(ctx context.Context, id int64, isActive bool) (*Staff, error)

	// UpdatePassword replaces the stored bcrypt hash for a staff account.
	//
	// currentPassword is the acting super-admin's own password, used as a
	// confirmation step; newPassword is the value to set on the target
	// account. Both are required. See StaffService.UpdatePassword for the
	// re-authentication and password-policy semantics.
	UpdatePassword(ctx context.Context, id int64, currentPassword, newPassword string) error

	// Delete removes a staff account. See StaffService.Delete for the
	// self-deletion guard.
	Delete(ctx context.Context, id int64) error
}

// StaffHandler handles HTTP requests for staff management.
//
// It is the HTTP adapter for the staff feature: it parses URL path
// parameters and query strings, decodes and validates JSON request
// bodies, delegates business logic to a StaffServicer, and maps the
// service's domain errors to HTTP status codes and the standard response
// envelope.
//
// # Dependency on an interface
//
// As documented on StaffServicer, this handler depends on an interface
// rather than on the concrete *StaffService type. Every other handler in
// the codebase wires directly to a concrete service. The interface is a
// testability seam; the concrete implementation passed by the module is
// *StaffService.
//
// # Error mapping
//
// Each method recognises a small set of service sentinels
// (ErrStaffNotFound, ErrEmailAlreadyRegistered, ErrEmailAlreadyUsed,
// ErrInvalidRole, ErrCannotDeleteSelf, ErrForbidden,
// ErrInvalidCurrentPassword, ErrPasswordTooShort, ErrPasswordTooLong)
// and maps them to status codes. Any unrecognised error — including
// repository failures and wrapped errors from the service — falls
// through to a 500 whose response body is the generic string "internal
// server error". This is the same disclosure policy used by the
// notifications, mobile, segments, and profile features; the auth
// feature also uses a generic message.
//
// # Concurrency
//
// A StaffHandler holds only a StaffServicer and is safe for concurrent
// use. net/http invokes Handle methods from a distinct goroutine per
// connection; the handler shares no mutable state across those
// invocations.
type StaffHandler struct {
	service StaffServicer
}

// NewStaffHandler creates a new StaffHandler instance.
//
// service must be non-nil; it is stored directly and dereferenced on the
// first request, so a nil value will panic at request time rather than at
// construction. In practice the service is wired by the staff module's
// constructor from the concrete *StaffService; a test may pass a fake.
func NewStaffHandler(service StaffServicer) *StaffHandler {
	return &StaffHandler{
		service: service,
	}
}

// Create handles POST /api/v1/staff.
// It validates the request, creates a new staff user, and returns 201 Created.
// Returns 400 for validation errors, 403 if the acting staff user is not a
// super admin, 409 for duplicate email, and 500 for internal errors.
//
// # Validation
//
// The handler decodes the body and rejects a request where any of Role,
// Name, Email, or Password is empty. Deeper validation — role existence,
// email uniqueness, password strength — is the service's responsibility.
//
// # Authorisation
//
// The service enforces the super-admin invariant independently of the
// HTTP layer (see StaffService.Create and ErrForbidden). In the current
// wiring the route is also gated by current-DB staff.manage permission, so a
// caller that reaches this handler has already been checked once; the
// service check is defense in depth. When the service returns
// ErrForbidden, the handler maps it to 403 Forbidden.
//
// # Responses
//
//   - 400 Bad Request: the body could not be decoded, a required field
//     was empty, or the service returned ErrInvalidRole (the role name
//     did not resolve to a row).
//   - 403 Forbidden: the service returned ErrForbidden — the acting
//     staff user is not a super admin. Missing/inactive identity returns 401.
//   - 409 Conflict: the service returned ErrEmailAlreadyRegistered — a
//     staff user with the same email already exists. The check is
//     advisory; a concurrent create with the same email can race past
//     it, and the resulting outcome depends on the schema's unique
//     constraint — see the service's Create for details.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 201 Created: the created StaffResponse, under the message "staff
//     created".
//
// # Concurrency
//
// Safe for concurrent use.
func (h *StaffHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateStaffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}
	if req.Role == "" || req.Name == "" || req.Email == "" || req.Password == "" {
		response.JSON(w, http.StatusBadRequest, nil, "missing required fields")
		return
	}

	staff, err := h.service.Create(r.Context(), CreateStaffParams{
		Role:     req.Role,
		Name:     req.Name,
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		if authz.WriteError(w, err) {
			return
		}
		switch {
		case errors.Is(err, ErrEmailAlreadyRegistered):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		case errors.Is(err, ErrInvalidRole):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		case errors.Is(err, ErrForbidden):
			response.JSON(w, http.StatusForbidden, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	response.JSON(w, http.StatusCreated, toStaffResponse(staff), "staff created")
}

// GetByID handles GET /api/v1/staff/{id}.
// It retrieves a staff user by ID and returns 200 OK with staff data.
// Returns 404 if the staff user is not found.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or not a valid
//     int64.
//   - 404 Not Found: the service returned ErrStaffNotFound.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 200 OK: the StaffResponse for the identified user.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *StaffHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}
	staff, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		if authz.WriteError(w, err) {
			return
		}
		if errors.Is(err, ErrStaffNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		return
	}
	response.JSON(w, http.StatusOK, toStaffResponse(staff), "success")
}

// List handles GET /api/v1/staff.
// It returns a paginated list of staff users with optional search by name/email.
// Query params: page, limit, search.
//
// # Query parameters
//
// page and limit are parsed by getPaginationParams with the defaults and
// clamping policy documented on that helper. search is an optional
// keyword passed through to the service; an empty search selects the
// unfiltered list path.
//
// # TotalPages computation
//
// The handler computes TotalPages as ceiling division of Total by Limit.
// Unlike the segments and notifications features, which leave this
// computation to the client, this response carries the pre-computed
// value. The two conventions coexist in the codebase; a client that
// consumes multiple list endpoints must not assume a uniform shape.
//
// # Responses
//
//   - 200 OK: a ListStaffResponse with the current page, total, and page
//     count.
//   - 500 Internal Server Error: any service error. The response body is
//     the generic string "internal server error"; the underlying error
//     is not disclosed.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *StaffHandler) List(w http.ResponseWriter, r *http.Request) {
	page, limit := getPaginationParams(r)
	search := r.URL.Query().Get("search")

	items, total, err := h.service.List(r.Context(), page, limit, search)
	if err != nil {
		if authz.WriteError(w, err) {
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		return
	}

	data := make([]StaffResponse, len(items))
	for i, item := range items {
		data[i] = toStaffResponse(&item)
	}

	totalPages := int32((total + int64(limit) - 1) / int64(limit))
	resp := ListStaffResponse{
		Data:       data,
		Total:      total,
		Page:       page,
		Limit:      limit,
		TotalPages: totalPages,
	}
	response.JSON(w, http.StatusOK, resp, "success")
}

// Update handles PATCH /api/v1/staff/{id}.
// It updates a staff user's role, name, or email.
// Returns 200 OK with updated staff data, 404 if not found, 400 for invalid input.
//
// # Validation
//
// The handler decodes the body and rejects a request where all three of
// Role, Name, and Email are empty. Because the DTO's fields are plain
// strings, an empty value for one field means "leave that field
// unchanged" at the service layer — a client that supplies only Role
// leaves Name and Email untouched. A client that wants to change a field
// to the empty string cannot do so through this API.
//
// # Responses
//
//   - 400 Bad Request: a malformed id, a JSON decode failure, a body with
//     all three fields empty, or the service returned ErrInvalidRole.
//   - 404 Not Found: the service returned ErrStaffNotFound.
//   - 409 Conflict: the service returned ErrEmailAlreadyUsed — the new
//     email collides with another staff user's email.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 200 OK: the updated StaffResponse, under the message "staff
//     updated".
//
// # Concurrency
//
// Safe for concurrent use.
func (h *StaffHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}
	var req UpdateStaffRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}
	if req.Role == "" && req.Name == "" && req.Email == "" {
		response.JSON(w, http.StatusBadRequest, nil, "at least one field must be provided")
		return
	}

	staff, err := h.service.Update(r.Context(), UpdateStaffParams{
		ID:    id,
		Role:  req.Role,
		Name:  req.Name,
		Email: req.Email,
	})
	if err != nil {
		if authz.WriteError(w, err) {
			return
		}
		switch {
		case errors.Is(err, ErrStaffNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrInvalidRole):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		case errors.Is(err, ErrEmailAlreadyUsed):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}
	response.JSON(w, http.StatusOK, toStaffResponse(staff), "staff updated")
}

// UpdateStatus handles PATCH /api/v1/staff/{id}/status.
// It updates the active status of a staff user.
// Returns 200 OK with updated staff data, 404 if not found.
//
// # Absent-field behaviour
//
// Because UpdateStatusRequest.IsActive is a plain bool, a request body
// that omits the field is indistinguishable from a body that explicitly
// sets it to false. Both deactivate the account. A client that intends to
// leave the status unchanged cannot express that intent through this
// endpoint.
//
// # Responses
//
//   - 400 Bad Request: a malformed id or a JSON decode failure.
//   - 404 Not Found: the service returned ErrStaffNotFound.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 200 OK: the updated StaffResponse, under the message "status
//     updated".
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent status changes on the same
// account are not serialised; the last write wins.
func (h *StaffHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}
	var req UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}
	staff, err := h.service.UpdateStatus(r.Context(), id, req.IsActive)
	if err != nil {
		if authz.WriteError(w, err) {
			return
		}
		if errors.Is(err, ErrStaffNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		return
	}
	response.JSON(w, http.StatusOK, toStaffResponse(staff), "status updated")
}

// UpdatePassword handles PATCH /api/v1/staff/{id}/password.
// It updates the password of a staff user.
// Returns 200 OK on success; 400 if the id is malformed, the body is
// malformed, either password field is empty, or the new password violates
// the length policy; 403 if the acting staff user is not a super admin or
// the current-password confirmation fails; 404 if the target account does
// not exist; 500 for internal errors.
//
// # Administrative reset with re-authentication
//
// This is an administrative password reset: the caller sets a new password
// for the target account. The target's current password is not required —
// the caller is acting on someone else's account — but the *actor's* own
// current password is required as a confirmation step. The handler
// forwards both values to the service and does not itself perform any
// verification; the service re-authenticates the actor and enforces the
// password-length policy. See StaffService.UpdatePassword and
// UpdatePasswordRequest for the rationale.
//
// # Validation performed here
//
// The handler rejects a request where either CurrentPassword or Password
// is empty with a 400 before invoking the service. Deeper checks —
// whether the current password is correct, whether the new password
// satisfies the length policy, whether the target account exists — are
// the service's responsibility and are reported through the error
// mapping below.
//
// # Existing sessions
//
// A successful password change does not invalidate the target account's
// existing access or refresh tokens. Any active session on the account
// continues to work until its tokens expire; the new password is required
// only for the next login. This is the codebase's no-revocation model,
// and it means an admin-triggered password reset is not sufficient on its
// own to force a compromised session to end.
//
// # Responses
//
//   - 400 Bad Request: a malformed id, a JSON decode failure, an empty
//     current_password or password, or a new password that violates the
//     length policy (ErrPasswordTooShort / ErrPasswordTooLong).
//   - 403 Forbidden: the service returned ErrForbidden — the acting
//     staff user is not a super admin. Missing/inactive identity returns 401. Also returned for
//     ErrInvalidCurrentPassword — the current_password value did not
//     match the acting super-admin's stored hash.
//   - 404 Not Found: the service returned ErrStaffNotFound.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 200 OK: no data payload; the message "password updated" accompanies
//     a nil data field.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *StaffHandler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}
	var req UpdatePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}
	if req.CurrentPassword == "" {
		response.JSON(w, http.StatusBadRequest, nil, "current password required")
		return
	}
	if req.Password == "" {
		response.JSON(w, http.StatusBadRequest, nil, "password required")
		return
	}
	if err := h.service.UpdatePassword(r.Context(), id, req.CurrentPassword, req.Password); err != nil {
		if authz.WriteError(w, err) {
			return
		}
		switch {
		case errors.Is(err, ErrStaffNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrForbidden), errors.Is(err, ErrInvalidCurrentPassword):
			response.JSON(w, http.StatusForbidden, nil, err.Error())
		case errors.Is(err, ErrPasswordTooShort), errors.Is(err, ErrPasswordTooLong):
			response.JSON(w, http.StatusBadRequest, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}
	response.JSON(w, http.StatusOK, nil, "password updated")
}

// Delete handles DELETE /api/v1/staff/{id}.
// It removes a staff user from the system.
// Returns 200 OK on successful deletion, 400 for invalid ID format,
// 403 if the actor attempts to delete our own account,
// 404 if the staff user does not exist, and 500 for internal errors.
//
// # Self-deletion guard
//
// The handler does not itself check whether the acting staff user is the
// target of the delete. That check is performed by the service, which
// reads the acting identity from the request context and compares it to
// the path's id. When the two match, the service returns
// ErrCannotDeleteSelf, which the handler maps to 403 Forbidden.
//
// The reason for the guard is practical: a super-admin who deleted their
// own account would be unable to log in again, and no other account may
// exist to restore them. The service prevents the lockout at the source.
//
// # Responses
//
//   - 400 Bad Request: the path's id parameter is missing or malformed.
//   - 403 Forbidden: the service returned ErrCannotDeleteSelf — the
//     acting staff user attempted to delete their own account.
//   - 404 Not Found: the service returned ErrStaffNotFound.
//   - 500 Internal Server Error: any other service error. The response
//     body is the generic string "internal server error"; the underlying
//     error is not disclosed.
//   - 200 OK: no data payload; the message "staff user deleted"
//     accompanies a nil data field.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *StaffHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64FromPath(r, "id")
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid id")
		return
	}
	if err = h.service.Delete(r.Context(), id); err != nil {
		if authz.WriteError(w, err) {
			return
		}
		switch {
		case errors.Is(err, ErrStaffNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrCannotDeleteSelf):
			response.JSON(w, http.StatusForbidden, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}
	response.JSON(w, http.StatusOK, nil, "staff user deleted")
}

// toStaffResponse converts a domain Staff object to a StaffResponse DTO.
//
// The conversion is a field-by-field copy with one transformation beyond
// the direct assignment: CreatedAt and UpdatedAt are formatted to RFC3339
// strings. This is why StaffResponse's timestamp fields are typed as
// string rather than time.Time — the formatting is done here rather than
// relying on encoding/json's default time.Time marshalling.
//
// The two encodings produce the same wire format for UTC timestamps, so no
// client-visible difference arises from this choice. The function is
// unexported because the conversion is an implementation detail of this
// package's response shaping.
//
// s must be non-nil; the function dereferences it immediately. In the
// current call sites the service guarantees a non-nil Staff on a nil
// error, so the precondition is honoured by construction.
func toStaffResponse(s *Staff) StaffResponse {
	return StaffResponse{
		ID:        s.ID,
		RoleID:    s.RoleID,
		RoleName:  s.RoleName,
		Name:      s.Name,
		Email:     s.Email,
		IsActive:  s.IsActive,
		CreatedAt: s.CreatedAt.Format(time.RFC3339),
		UpdatedAt: s.UpdatedAt.Format(time.RFC3339),
	}
}

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
// negative number is accepted and returned as-is. It also does not
// distinguish "the parameter was missing" from "the parameter was not a
// number" — both produce an error whose message is "missing id".
//
// # Duplication
//
// This is at least the fifth copy of this helper in the codebase — the
// others appear in the notifications, segments, and (depending on the
// file layout) mobile and users features. All five are unexported and
// identical. A future refactor that extracts these into a shared
// request-parsing helper package would collapse the copies into one.
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
// Clients should treat the values echoed in the response's Page and Limit
// fields as authoritative, not the values they sent.
//
// # Duplication
//
// This function is byte-for-byte identical to the versions in
// notifications, segments, and (if present) mobile and users. As with
// parseInt64FromPath, the duplication is due to the helpers being
// unexported in each package; a shared request-parsing helper package
// would collapse them.
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
