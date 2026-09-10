package profile

import (
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all profile endpoints with the provided ServeMux.
//
// # Endpoints
//
//   - GET   /api/v1/profile           — ProfileHandler.GetProfile
//   - PATCH /api/v1/profile           — ProfileHandler.UpdateProfile
//   - PATCH /api/v1/profile/password  — ProfileHandler.UpdatePassword
//
// Every route is wrapped by the module's middleware chain, which the caller
// supplied at construction time — in the current wiring the API-key and
// JWT middlewares. The staff identity the handlers rely on
// (GetStaffIDFromContext) is set by the JWT middleware, so the JWT
// middleware must be present in the chain for these routes to be usable;
// a chain that omits it will produce 401 responses from every handler.
//
// # Prefix convention
//
// Unlike the other feature modules — which declare a full feature prefix
// such as "/api/v1/notifications" or "/api/v1/auth" — this module declares
// only "/api/v1" and appends the "profile" segment per route. The two
// conventions are equivalent in effect; the difference is stylistic and
// worth knowing when scanning route registrations across the codebase.
//
// # Routing syntax
//
// The patterns use the method-aware ServeMux syntax introduced in Go 1.22.
// A GET to a PATCH-only route and vice versa will be answered with 405
// rather than being routed to a handler. Note that
// /api/v1/profile/password is a PATCH-only route distinct from
// /api/v1/profile; the ServeMux will not confuse the two.
//
// # Concurrency
//
// RegisterRoutes is expected to be invoked once during startup, before the
// HTTP server begins listening. It is not safe to call concurrently with
// requests being served.
func (m *ProfileModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1"

	use := middleware.Chain(m.middlewares...)

	mux.HandleFunc("GET "+prefix+"/profile", use(m.handler.GetProfile))
	mux.HandleFunc("PATCH "+prefix+"/profile", use(m.handler.UpdateProfile))
	mux.HandleFunc("PATCH "+prefix+"/profile/password", use(m.handler.UpdatePassword))
}
