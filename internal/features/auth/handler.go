package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"hello/internal/token"
	"hello/pkg/response"
)

// AuthHandler handles HTTP requests for authentication endpoints.
//
// It is a thin adapter: the handler parses the JSON request body, delegates
// to the AuthService, and maps the service's domain errors to HTTP status
// codes and the standard response envelope. No business logic lives here,
// and no persistence calls are made directly.
//
// # Error mapping
//
// Both endpoints follow the same pattern. A JSON decode failure yields 400
// with a generic "invalid request body" message. A recognised domain error
// yields the status code appropriate to that error. Every other error —
// database connectivity, unexpected service failure — yields a generic 500
// with "internal server error" and no detail, so that internal state is not
// leaked to the client. The handler does not log the underlying error; if a
// future revision adds logging, it belongs here rather than in the service,
// because this is the layer that has access to the request.
//
// # Concurrency
//
// An AuthHandler holds only a pointer to its service and is safe for
// concurrent use. net/http invokes Handle methods from a distinct goroutine
// per connection; the handler does not share mutable state across those
// invocations.
type AuthHandler struct {
	service *AuthService
}

// NewAuthHandler creates a new AuthHandler instance.
//
// service must be non-nil; it is stored directly and dereferenced on the
// first request, so a nil value will panic at request time rather than at
// construction. The caller is expected to construct the service once at
// startup (via the module wiring in module.go) and to share the single
// instance across all requests.
func NewAuthHandler(service *AuthService) *AuthHandler {
	return &AuthHandler{
		service: service,
	}
}

// Login handles POST /api/v1/auth/login.
// It authenticates staff credentials and returns access/refresh tokens.
//
// # Request
//
// The body must be a JSON object matching LoginRequest. The handler reads it
// with json.NewDecoder(r.Body).Decode. There is no explicit body size limit
// and no Content-Type check: a sufficiently large body will consume memory
// up to whatever limit the surrounding net/http server imposes (by default,
// none), and a body sent with a non-JSON Content-Type will still be
// decoded. Deployments that expose this endpoint to untrusted networks
// should add a body-size limit and a Content-Type guard in front of the
// handler; the README lists input sanitization as a not-yet-implemented
// concern.
//
// # Responses
//
//   - 400 Bad Request: the request body could not be decoded as JSON.
//   - 401 Unauthorized: the credentials did not match. The message is
//     taken from ErrInvalidCredentials — currently "invalid credentials" —
//     and is deliberately identical for "no such user" and "wrong
//     password" so that an attacker cannot enumerate staff emails.
//   - 500 Internal Server Error: any other service error. The response
//     carries no detail.
//   - 200 OK: a LoginResponse with access and refresh tokens, wrapped in
//     the standard response envelope.
//
// # Concurrency
//
// Safe for concurrent use. Each invocation operates on its own request and
// response writer.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	res, err := h.service.Login(
		r.Context(),
		req.Email,
		req.Password,
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidCredentials):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	response.JSON(w, http.StatusOK, LoginResponse{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
	}, "success")
}

// RefreshToken handles POST /api/v1/auth/refresh.
// It accepts a valid refresh token and returns a new access token.
//
// # Request
//
// The body must be a JSON object matching RefreshTokenRequest. As with
// Login, there is no body-size limit and no Content-Type check; the same
// deployment-level hardening applies.
//
// # Responses
//
//   - 400 Bad Request: the request body could not be decoded as JSON.
//   - 401 Unauthorized: the refresh token was missing, malformed, expired,
//     signed with the wrong secret, or of the wrong class (for example, an
//     access token presented as a refresh token); or the token was valid but
//     the referenced staff account no longer exists or has been deactivated.
//     The first class collapses to token.ErrInvalidToken and the second to
//     ErrInvalidCredentials, but both are served the same 401 status so the
//     client cannot distinguish the cases.
//   - 500 Internal Server Error: any other service error — a database or
//     role-resolution failure during token refresh.
//   - 200 OK: a RefreshTokenResponse with a newly minted access token. The
//     refresh token is not rotated and is not returned again.
//
// # Response shape
//
// Unlike Login, this handler returns res directly rather than constructing
// the response struct field by field. That is only correct because
// AuthService.RefreshToken happens to return *RefreshTokenResponse — a
// future change that alters the service's return type would silently change
// the wire shape of this endpoint. If the service is refactored, this
// handler must be updated in lockstep; the two are coupled by the concrete
// return type, not by any interface contract.
//
// # Concurrency
//
// Safe for concurrent use. Each invocation operates on its own request and
// response writer.
func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req RefreshTokenRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return
	}

	res, err := h.service.RefreshToken(
		r.Context(),
		req.RefreshToken,
	)
	if err != nil {
		switch {
		case errors.Is(err, token.ErrInvalidToken), errors.Is(err, ErrInvalidCredentials):
			response.JSON(w, http.StatusUnauthorized, nil, err.Error())
		default:
			response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
		}
		return
	}

	response.JSON(w, http.StatusOK, res, "success")
}
