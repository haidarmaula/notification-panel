package staff

import (
	"hello/internal/authz"
	"hello/internal/middleware"
	"net/http"
)

// RegisterRoutes registers all staff endpoints with the provided ServeMux.
//
// # Endpoints
//
//   - GET    /api/v1/staff                    — StaffHandler.List
//   - GET    /api/v1/staff/{id}               — StaffHandler.GetByID
//   - POST   /api/v1/staff                    — StaffHandler.Create
//   - PATCH  /api/v1/staff/{id}               — StaffHandler.Update
//   - PATCH  /api/v1/staff/{id}/status        — StaffHandler.UpdateStatus
//   - PATCH  /api/v1/staff/{id}/password      — StaffHandler.UpdatePassword
//   - DELETE /api/v1/staff/{id}               — StaffHandler.Delete
//
// Every route checks current database staff.manage permission, granted only to
// active SUPER_ADMIN accounts, inside the supplied API-key/JWT/audit chain.
// The service independently rechecks; JWT RoleName is never permission authority.
//
// # Prefix convention
//
// As with the profile module, this module declares only the "/api/v1"
// prefix and appends the "staff" segment per route, rather than declaring
// the full "/api/v1/staff" prefix as a constant. The two conventions are
// equivalent in effect; the difference is stylistic and worth knowing
// when scanning route registrations across the codebase.
//
// # Routing syntax
//
// The patterns use the method-aware ServeMux syntax introduced in Go 1.22.
// A GET to a PATCH-only route and vice versa is answered with 405 rather
// than being routed. The {id} segment is a wildcard whose value is read
// by the handlers via r.PathValue.
//
// # Concurrency
//
// RegisterRoutes is expected to be invoked once during startup, before the
// HTTP server begins listening. It is not safe to call concurrently with
// requests being served.
func (m *StaffModule) RegisterRoutes(mux *http.ServeMux) {
	const prefix = "/api/v1"

	use := middleware.Chain(m.middlewares...)

	mux.HandleFunc("GET "+prefix+"/staff", use(authz.Guard(m.permissions, authz.StaffManage, m.handler.List)))
	mux.HandleFunc("GET "+prefix+"/staff/{id}", use(authz.Guard(m.permissions, authz.StaffManage, m.handler.GetByID)))
	mux.HandleFunc("POST "+prefix+"/staff", use(authz.Guard(m.permissions, authz.StaffManage, m.handler.Create)))
	mux.HandleFunc("PATCH "+prefix+"/staff/{id}", use(authz.Guard(m.permissions, authz.StaffManage, m.handler.Update)))
	mux.HandleFunc("PATCH "+prefix+"/staff/{id}/status", use(authz.Guard(m.permissions, authz.StaffManage, m.handler.UpdateStatus)))
	mux.HandleFunc("PATCH "+prefix+"/staff/{id}/password", use(authz.Guard(m.permissions, authz.StaffManage, m.handler.UpdatePassword)))
	mux.HandleFunc("DELETE "+prefix+"/staff/{id}", use(authz.Guard(m.permissions, authz.StaffManage, m.handler.Delete)))
}
