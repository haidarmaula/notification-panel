package profile

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"hello/pkg/response"
)

// ProfileHandler handles HTTP requests for staff profile management.
//
// It is the HTTP adapter for the profile feature: it extracts the acting
// staff user's identity from the request context, decodes and validates the
// JSON request bodies, delegates to ProfileService, and maps the service's
// domain errors to status codes and the standard response envelope. No
// business logic and no persistence calls live here.
//
// # Identity comes from the context, not the URL
//
// Every method reads the acting staff ID via GetStaffIDFromContext rather
// than from a path parameter. A request with an authenticated staff user
// can therefore only act on that user's own account; there is no route that
// lets a staff user edit another user's profile. Administrative edits of
// other staff accounts are the staff feature's responsibility, not this
// one's.
//
// # Error mapping
//
// Each method recognises a small set of service sentinels (ErrProfileNotFound,
// ErrEmailAlreadyUsed, ErrInvalidCredentials) and maps them to specific
// status codes. Any unrecognised error — including repository failures and
// wrapped errors from the service — falls through to a 500 whose response
// body carries err.Error() verbatim. This is the same disclosure pattern
// used by the notifications and mobile features; the auth feature is the
// only handler that replaces the 500 message with a generic string.
//
// # Concurrency
//
// A ProfileHandler holds only a pointer to its service and is safe for
// concurrent use. net/http invokes Handle methods from a distinct goroutine
// per connection; the handler shares no mutable state across those
// invocations.
type ProfileHandler struct {
	service *ProfileService
}

// NewProfileHandler creates a new ProfileHandler instance.
//
// service must be non-nil; it is stored directly and dereferenced on the
// first request, so a nil value will panic at request time rather than at
// construction. In practice the service is wired by the profile module's
// constructor from the shared dependencies.
func NewProfileHandler(service *ProfileService) *ProfileHandler {
	return &ProfileHandler{service: service}
}

// GetProfile handles GET /api/v1/profile.
// It returns the profile of the authenticated staff user.
//
// # Flow
//
// The method extracts the acting staff ID from the request context and
// rejects the request with 401 if no identity is present. It then delegates
// to the service, which loads the staff row and joins the role name. On
// success the domain Profile is converted to a ProfileResponse by
// toProfileResponse.
//
// # Responses
//
//   - 401 Unauthorized: the request context carries no staff identity, or
//     the identity is a zero value. The response message is the literal
//     string "unauthorized".
//   - 404 Not Found: the service returned ErrProfileNotFound — the staff
//     user identified by the token no longer exists in the database.
//   - 500 Internal Server Error: any other service error, served with
//     err.Error() as the message.
//   - 200 OK: a ProfileResponse, wrapped in the standard envelope with the
//     message "success".
//
// # Concurrency
//
// Safe for concurrent use.
func (h *ProfileHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	staffID, ok := GetStaffIDFromContext(r.Context())
	if !ok || staffID == 0 {
		response.JSON(w, http.StatusUnauthorized, nil, "unauthorized")
		return
	}

	profile, err := h.service.GetProfile(r.Context(), staffID)
	if err != nil {
		if errors.Is(err, ErrProfileNotFound) {
			response.JSON(w, http.StatusNotFound, nil, err.Error())
			return
		}
		response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		return
	}

	response.JSON(w, http.StatusOK, toProfileResponse(profile), "success")
}

// UpdateProfile handles PATCH /api/v1/profile.
// It updates the name and/or email of the authenticated staff user.
//
// # Validation
//
// The method extracts the acting staff ID, decodes the body into an
// UpdateProfileRequest, and rejects a request where both Name and Email are
// empty. Note that because the DTO's fields are plain strings, a request
// that supplies only one of the two passes the empty string for the other
// to the service; see UpdateProfileRequest for the consequences and the
// recommended client pattern.
//
// # Responses
//
//   - 401 Unauthorized: the request context carries no staff identity.
//   - 400 Bad Request: the body could not be decoded, or both Name and
//     Email were empty.
//   - 404 Not Found: the service returned ErrProfileNotFound.
//   - 409 Conflict: the service returned ErrEmailAlreadyUsed — the new
//     email collides with another staff user's email. The service is
//     expected to have checked for this before attempting the write;
//     whether the check is atomic with the write is the service's concern,
//     not the handler's.
//   - 500 Internal Server Error: any other service error, served with
//     err.Error() as the message.
//   - 200 OK: the updated ProfileResponse, with the message "profile
//     updated". Unlike the equivalent response in the notifications
//     feature, this response echoes the updated state rather than a bare
//     message, so a client does not need a follow-up GET.
//
// # Concurrency
//
// Safe for concurrent use.
func (h *ProfileHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	staffID, ok := GetStaffIDFromContext(r.Context())
	if !ok || staffID == 0 {
		response.JSON(w, http.StatusUnauthorized, nil, "unauthorized")
		return
	}

	var req UpdateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.Name == "" && req.Email == "" {
		response.JSON(w, http.StatusBadRequest, nil, "at least one field (name or email) must be provided")
		return
	}

	profile, err := h.service.UpdateProfile(r.Context(), UpdateProfileParams{
		ID:    staffID,
		Name:  req.Name,
		Email: req.Email,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrProfileNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrEmailAlreadyUsed):
			response.JSON(w, http.StatusConflict, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, toProfileResponse(profile), "profile updated")
}

// UpdatePassword handles PATCH /api/v1/profile/password.
// It updates the password of the authenticated staff user.
//
// # Validation
//
// The method extracts the acting staff ID, decodes the body into an
// UpdatePasswordRequest, and rejects a request with either CurrentPassword
// or NewPassword empty. It performs no password-strength or length check;
// see UpdatePasswordRequest for the policy implications.
//
// # Responses
//
//   - 401 Unauthorized: the request context carries no staff identity, or
//     the service returned ErrInvalidCredentials because CurrentPassword
//     did not match the stored hash.
//   - 400 Bad Request: the body could not be decoded, or either password
//     field was empty.
//   - 404 Not Found: the service returned ErrProfileNotFound.
//   - 500 Internal Server Error: any other service error, served with
//     err.Error() as the message.
//   - 200 OK: no data payload; the message "password updated successfully"
//     accompanies a nil data field.
//
// # What happens to existing sessions
//
// A successful password change does not invalidate the caller's existing
// access or refresh tokens. The tokens in the current session continue to
// work until they expire; the new password is required only for the next
// login. This mirrors the codebase's broader no-revocation model (see the
// token package) and means a password change does not end a session on
// another device.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent password changes with the same
// current password will both pass the verification step, both write a new
// hash, and whichever write lands last wins. The caller whose update did
// not land last has effectively been logged out of the change they just
// made, in the sense that their intended new password is not the one now
// stored.
func (h *ProfileHandler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	staffID, ok := GetStaffIDFromContext(r.Context())
	if !ok || staffID == 0 {
		response.JSON(w, http.StatusUnauthorized, nil, "unauthorized")
		return
	}

	var req UpdatePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	if req.CurrentPassword == "" || req.NewPassword == "" {
		response.JSON(w, http.StatusBadRequest, nil, "current password and new password are required")
		return
	}

	if err := h.service.UpdatePassword(r.Context(), staffID, req.CurrentPassword, req.NewPassword); err != nil {
		switch {
		case errors.Is(err, ErrProfileNotFound):
			response.JSON(w, http.StatusNotFound, nil, err.Error())
		case errors.Is(err, ErrInvalidCredentials):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, err.Error())
		}
		return
	}

	response.JSON(w, http.StatusOK, nil, "password updated successfully")
}

// toProfileResponse converts domain Profile to response DTO.
//
// It is a straightforward field-by-field mapping, with one transformation
// beyond the direct copy: CreatedAt and UpdatedAt are formatted to RFC3339
// strings. This is why ProfileResponse.CreatedAt and ProfileResponse.UpdatedAt
// are typed as string rather than time.Time — the conversion is done here
// rather than relying on encoding/json's default time.Time marshalling.
//
// The two encodings produce the same wire format for UTC timestamps, so no
// client-visible difference arises from this choice. The function is
// unexported because the conversion is an implementation detail of this
// package's response shaping; callers outside the package see only the DTO.
//
// p must be non-nil; the function dereferences it immediately. In the
// current call sites the service guarantees a non-nil profile on a nil
// error, so the precondition is honoured by construction.
func toProfileResponse(p *Profile) ProfileResponse {
	return ProfileResponse{
		ID:        p.ID,
		RoleID:    p.RoleID,
		RoleName:  p.RoleName,
		Name:      p.Name,
		Email:     p.Email,
		IsActive:  p.IsActive,
		CreatedAt: p.CreatedAt.Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.Format(time.RFC3339),
	}
}
