package notifications

import (
	"hello/internal/authz"
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all notification endpoints with the provided ServeMux.
//
// # Endpoints
//
//   - GET    /api/v1/notifications          — NotificationHandler.List
//   - GET    /api/v1/notifications/{id}     — NotificationHandler.GetByID
//   - POST   /api/v1/notifications          — NotificationHandler.Create
//   - PATCH  /api/v1/notifications/{id}     — NotificationHandler.Update
//   - DELETE /api/v1/notifications/{id}     — NotificationHandler.Delete
//   - POST   /api/v1/notifications/{id}/send — NotificationHandler.Send
//
// Every route is wrapped by the module's middleware chain, which the caller
// supplied at construction time — in the current wiring the API-key, JWT,
// and audit middlewares. There is no unauthenticated route in this feature.
//
// # Routing syntax
//
// The patterns use the method-aware ServeMux syntax introduced in Go 1.22.
// A GET to POST-only routes and vice versa will be answered with a 405
// rather than being routed to a handler. The `{id}` segment is a wildcard
// whose value is read by the handler via r.PathValue.
//
// # Route ownership
//
// The prefix constant is local to this function, so versioning the API
// (for example, to /api/v2) is a one-line change confined to this file.
// The module does not overwrite routes registered by other modules.
//
// # Concurrency
//
// RegisterRoutes is expected to be invoked once during startup, before the
// HTTP server begins listening. It is not safe to call concurrently with
// requests being served.
func (m *NotificationModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1/notifications"
	use := middleware.Chain(m.middlewares...)

	mux.HandleFunc("GET "+prefix, use(authz.Guard(m.authorization, authz.NotificationsRead, m.handler.List)))
	mux.HandleFunc("GET "+prefix+"/{id}", use(authz.Guard(m.authorization, authz.NotificationsRead, m.handler.GetByID)))
	mux.HandleFunc("POST "+prefix, use(authz.Guard(m.authorization, authz.NotificationsWrite, m.handler.Create)))
	mux.HandleFunc("PATCH "+prefix+"/{id}", use(authz.Guard(m.authorization, authz.NotificationsWrite, m.handler.Update)))
	mux.HandleFunc("DELETE "+prefix+"/{id}", use(authz.Guard(m.authorization, authz.NotificationsWrite, m.handler.Delete)))
	mux.HandleFunc("POST "+prefix+"/{id}/send", use(authz.Guard(m.authorization, authz.NotificationsSend, m.handler.Send)))
}
