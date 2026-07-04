package staff

import (
	"hello/internal/middleware"
	"net/http"
)

func (m *StaffModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1"

	use := middleware.Chain(m.middlewares...)

	mux.HandleFunc("POST "+prefix+"/staff", use(m.handler.Create))
}
