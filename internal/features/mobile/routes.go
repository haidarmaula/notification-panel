package mobile

import (
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all mobile endpoints.
func (m *MobileModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1/mobile"
	use := middleware.Chain(m.middlewares...)

	mux.HandleFunc("POST "+prefix+"/sync", use(m.handler.Sync))
}
