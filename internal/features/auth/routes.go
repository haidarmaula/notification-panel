package auth

import (
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all authentication endpoints with the provided ServeMux.
//
// # Endpoints
//
//   - POST /api/v1/auth/login    — AuthHandler.Login
//   - POST /api/v1/auth/refresh  — AuthHandler.RefreshToken
//
// Both routes are registered through middleware.Chain, which composes the
// module's middlewares into a single wrapper applied to each handler. The
// caller supplies those middlewares at construction time; in the current
// wiring they consist of the API-key middleware only, and the JWT middleware
// is deliberately excluded because a client cannot possess a valid bearer
// token before calling login.
//
// # Routing syntax
//
// The patterns use the method-aware ServeMux syntax introduced in Go 1.22
// ("POST " + prefix + "/login"). A GET request to the same path will be
// answered by the mux with 405 Method Not Allowed rather than being routed
// to the handler.
//
// # Route ownership
//
// The module never overwrites routes registered by other modules: it only
// adds the two paths under its own prefix. The prefix constant is local to
// this function, so changing the API version path — for example, from
// /api/v1 to /api/v2 — is a one-line change confined to this file.
//
// # Concurrency
//
// RegisterRoutes is not safe to call concurrently with requests being
// served; it is expected to be invoked once during startup, before the HTTP
// server begins listening. It is safe to call multiple times with different
// muxes — for example, in tests — because it holds no per-mux state.
func (m *AuthModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1/auth"

	use := middleware.Chain(m.middlewares...)

	mux.HandleFunc("POST "+prefix+"/login", use(m.handler.Login))
	mux.HandleFunc("POST "+prefix+"/refresh", use(m.handler.RefreshToken))
}
