package segments

import (
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all segment endpoints with the provided ServeMux.
//
// # Endpoints
//
// Segment CRUD:
//
//   - GET    /api/v1/segments                    — SegmentHandler.List
//   - GET    /api/v1/segments/{id}               — SegmentHandler.GetByID
//   - POST   /api/v1/segments                    — SegmentHandler.Create
//   - PATCH  /api/v1/segments/{id}               — SegmentHandler.Update
//   - DELETE /api/v1/segments/{id}               — SegmentHandler.Delete
//
// Member sub-resource:
//
//   - GET    /api/v1/segments/{id}/members                   — MembersHandler.ListMembers
//   - POST   /api/v1/segments/{id}/members                   — MembersHandler.AddMember
//   - DELETE /api/v1/segments/{id}/members/{userId}          — MembersHandler.RemoveMember
//
// Every route is wrapped by the module's middleware chain, which the
// caller supplied at construction time — in the current wiring the
// API-key, JWT, and audit middlewares. There is no unauthenticated route
// in this feature.
//
// # Route ordering
//
// The ServeMux routes by pattern specificity, not by registration order,
// so the more specific /{id}/members routes take precedence over the
// generic /{id} patterns without any explicit ordering requirement. A
// request to GET /api/v1/segments/42/members is matched against the
// members route, not against the segment detail route with an id of
// "42/members".
//
// # Routing syntax
//
// The patterns use the method-aware ServeMux syntax introduced in Go 1.22.
// A non-matching method is answered with 405 rather than being routed.
// The {id} and {userId} segments are wildcards whose values are read by
// the handlers via r.PathValue — which is why the second parameter's key
// in RemoveMember's parseInt64FromPath call is "userId" (matching the
// pattern) rather than "user_id".
//
// # Concurrency
//
// RegisterRoutes is expected to be invoked once during startup, before the
// HTTP server begins listening. It is not safe to call concurrently with
// requests being served.
func (m *SegmentModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1/segments"
	use := middleware.Chain(m.middlewares...)

	// Segment routes
	mux.HandleFunc("GET "+prefix, use(m.handler.List))
	mux.HandleFunc("GET "+prefix+"/{id}", use(m.handler.GetByID))
	mux.HandleFunc("POST "+prefix, use(m.handler.Create))
	mux.HandleFunc("PATCH "+prefix+"/{id}", use(m.handler.Update))
	mux.HandleFunc("DELETE "+prefix+"/{id}", use(m.handler.Delete))

	// Member routes
	mux.HandleFunc("GET "+prefix+"/{id}/members", use(m.membersHandler.ListMembers))
	mux.HandleFunc("POST "+prefix+"/{id}/members", use(m.membersHandler.AddMember))
	mux.HandleFunc("DELETE "+prefix+"/{id}/members/{userId}", use(m.membersHandler.RemoveMember))
}
