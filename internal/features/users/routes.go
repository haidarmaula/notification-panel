package users

import (
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all user endpoints with the provided ServeMux.
//
// # Endpoints
//
// Administrative user views:
//
//   - GET    /api/v1/users                       — UserHandler.List
//   - GET    /api/v1/users/search                — UserHandler.Search
//   - GET    /api/v1/users/{id}                  — UserHandler.GetByID
//   - GET    /api/v1/users/{id}/segments         — UserHandler.GetUserSegments
//   - GET    /api/v1/users/{id}/notifications    — UserHandler.GetUserNotifications
//
// Device tokens, viewed through the user:
//
//   - GET    /api/v1/users/{id}/device-tokens    — DeviceTokenHandler.ListByUser
//
// Device tokens, managed directly (mobile-API style):
//
//   - POST   /api/v1/device-tokens               — DeviceTokenHandler.Register
//   - PATCH  /api/v1/device-tokens/{id}          — DeviceTokenHandler.Update
//   - DELETE /api/v1/device-tokens/{id}          — DeviceTokenHandler.Delete
//
// Every route is wrapped by the module's middleware chain, which the
// caller supplied at construction time — in the current wiring the
// API-key and JWT middlewares. There is no unauthenticated route in this
// feature, but note the divergence in trust model between the two
// families: the /users/* routes are admin-facing (the caller is expected
// to be an authenticated staff user), while the /device-tokens/* routes
// are the mobile-facing registration surface documented on
// DeviceTokenHandler, where authentication is by API key only and the
// target user ID is supplied by the caller.
//
// # Routing precedence
//
// The Go 1.22 ServeMux matches routes by pattern specificity, not by
// registration order. The literal /users/search pattern therefore takes
// precedence over the wildcard /users/{id} pattern regardless of the
// order in which they are registered. This is why the search route does
// not need to be declared before the {id} route; the current declaration
// order is chosen for readability rather than correctness.
//
// # Two prefixes in one module
//
// The module registers routes under two path prefixes: the user-facing
// /api/v1/users/* group and the top-level /api/v1/device-tokens group.
// The device-tokens group is not nested under /users/ because a device
// token is addressed by its own ID, not by the user it belongs to — the
// Register endpoint accepts the user ID from the request body rather than
// the URL.
//
// # Concurrency
//
// RegisterRoutes is expected to be invoked once during startup, before the
// HTTP server begins listening. It is not safe to call concurrently with
// requests being served.
func (m *UserModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1"
	use := middleware.Chain(m.middlewares...)

	// User endpoints (admin)
	mux.HandleFunc("GET "+prefix+"/users", use(m.handler.List))
	mux.HandleFunc("GET "+prefix+"/users/search", use(m.handler.Search))
	mux.HandleFunc("GET "+prefix+"/users/{id}", use(m.handler.GetByID))
	mux.HandleFunc("GET "+prefix+"/users/{id}/segments", use(m.handler.GetUserSegments))
	mux.HandleFunc("GET "+prefix+"/users/{id}/notifications", use(m.handler.GetUserNotifications))

	// User device tokens (admin view)
	mux.HandleFunc("GET "+prefix+"/users/{id}/device-tokens", use(m.deviceTokenHandler.ListByUser))

	// Device token management (mobile API)
	mux.HandleFunc("POST "+prefix+"/device-tokens", use(m.deviceTokenHandler.Register))
	mux.HandleFunc("PATCH "+prefix+"/device-tokens/{id}", use(m.deviceTokenHandler.Update))
	mux.HandleFunc("DELETE "+prefix+"/device-tokens/{id}", use(m.deviceTokenHandler.Delete))
}
