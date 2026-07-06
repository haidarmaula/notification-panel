package segments

import (
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all segment endpoints with the provided ServeMux.
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
