package mobile

import (
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all mobile endpoints.
//
// # Endpoints
//
//   - POST /api/v1/mobile/sync — MobileHandler.Sync
//
// The route is registered through middleware.Chain, which composes the
// module's middlewares into a single wrapper applied to the handler. The
// caller supplies those middlewares at construction time; in the current
// wiring they consist of the API-key middleware only, and the staff JWT
// middleware is deliberately excluded because the mobile client
// authenticates with a Laravel-issued token carried in the request body —
// not with a bearer token, and not with the same secret the staff-facing
// endpoints verify.
//
// # Routing syntax
//
// The pattern uses the method-aware ServeMux syntax introduced in Go 1.22
// ("POST " + prefix + "/sync"). A GET or other non-POST request to the same
// path will be answered by the mux with 405 Method Not Allowed rather than
// being routed to the handler.
//
// # Route ownership
//
// The prefix constant is local to this function, so changing the API
// version path — for example, from /api/v1 to /api/v2 — is a one-line
// change confined to this file. The module adds only the single sync route
// and does not overwrite routes registered by other modules.
//
// # Concurrency
//
// RegisterRoutes is not safe to call concurrently with requests being
// served; it is expected to be invoked once during startup, before the HTTP
// server begins listening.
func (m *MobileModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1/mobile"
	use := middleware.Chain(m.middlewares...)

	mux.HandleFunc("POST "+prefix+"/sync", use(m.handler.Sync))
}
