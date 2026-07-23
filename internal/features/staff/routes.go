package staff

import (
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all staff endpoints with the provided ServeMux.
func (m *StaffModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1"

	use := middleware.Chain(m.middlewares...)

	mux.HandleFunc("GET "+prefix+"/staff", use(m.handler.List))
	mux.HandleFunc("GET "+prefix+"/staff/{id}", use(m.handler.GetByID))
	mux.HandleFunc("POST "+prefix+"/staff", use(m.handler.Create))
	mux.HandleFunc("PATCH "+prefix+"/staff/{id}", use(m.handler.Update))
	mux.HandleFunc("PATCH "+prefix+"/staff/{id}/status", use(m.handler.UpdateStatus))
	mux.HandleFunc("PATCH "+prefix+"/staff/{id}/password", use(m.handler.UpdatePassword))
	mux.HandleFunc("DELETE "+prefix+"/staff/{id}", use(m.handler.Delete))
}
