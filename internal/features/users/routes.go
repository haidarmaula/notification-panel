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
// The /users/* routes use the admin middleware chain (API key plus staff
// JWT in the current wiring). The /device-tokens/* routes instead use the
// mobile middleware chain, which verifies a Laravel JWT and derives the
// caller's external user ID from its claims. No device-token operation
// trusts a user ID supplied by the request.
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
// token is addressed by its own ID, not by the user it belongs to. The
// authenticated user comes from the mobile JWT rather than the URL or body.
//
// # Concurrency
//
// RegisterRoutes is expected to be invoked once during startup, before the
// HTTP server begins listening. It is not safe to call concurrently with
// requests being served.
func (m *UserModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1"
	useAdmin := middleware.Chain(m.adminMiddlewares...)
	useDevice := middleware.Chain(m.deviceMiddlewares...)

	// User endpoints (admin)
	mux.HandleFunc("GET "+prefix+"/users", useAdmin(m.handler.List))
	mux.HandleFunc("GET "+prefix+"/users/search", useAdmin(m.handler.Search))
	mux.HandleFunc("GET "+prefix+"/users/{id}", useAdmin(m.handler.GetByID))
	mux.HandleFunc("GET "+prefix+"/users/{id}/segments", useAdmin(m.handler.GetUserSegments))
	mux.HandleFunc("GET "+prefix+"/users/{id}/notifications", useAdmin(m.handler.GetUserNotifications))

	// User device tokens (admin view)
	mux.HandleFunc("GET "+prefix+"/users/{id}/device-tokens", useAdmin(m.deviceTokenHandler.ListByUser))

	// Device token management (mobile API)
	mux.HandleFunc("POST "+prefix+"/device-tokens", useDevice(m.deviceTokenHandler.Register))
	mux.HandleFunc("PATCH "+prefix+"/device-tokens/{id}", useDevice(m.deviceTokenHandler.Update))
	mux.HandleFunc("DELETE "+prefix+"/device-tokens/{id}", useDevice(m.deviceTokenHandler.Delete))
}
