package applications

import (
	"hello/internal/authz"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"net/http"
)

// Module owns provisioning registry routes. No enrollment/activation/business
// routing is added. All handlers retain independent service authority checks.
type Module struct {
	handler     *Handler
	permissions authz.Checker
	middlewares []middleware.Middleware
}

func NewModule(q *sqlc.Queries, db Transactor, middlewares ...middleware.Middleware) *Module {
	return &Module{handler: NewHandler(NewService(q, db)), permissions: authz.New(repository.NewStaffUserRepository(q)), middlewares: middlewares}
}
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	use := middleware.Chain(m.middlewares...)
	register := func(pattern string, p authz.Permission, h http.HandlerFunc) {
		guarded := use(authz.Guard(m.permissions, p, h))
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			guarded(w, r)
		})
	}
	register("GET /api/v1/apps", authz.ApplicationsRead, m.handler.ListGranted)
	register("GET /api/v1/apps/{app_id}", authz.ApplicationsRead, m.handler.GetGranted)
	register("GET /api/v1/admin/apps", authz.ApplicationsManage, m.handler.ListManaged)
	register("POST /api/v1/admin/apps", authz.ApplicationsManage, m.handler.Create)
	register("GET /api/v1/admin/apps/{app_id}", authz.ApplicationsManage, m.handler.GetManaged)
	register("PATCH /api/v1/admin/apps/{app_id}", authz.ApplicationsManage, m.handler.Rename)
	register("GET /api/v1/admin/apps/{app_id}/staff-grants", authz.ApplicationsManage, m.handler.ListGrants)
	register("GET /api/v1/admin/apps/{app_id}/staff-grants/{staff_id}", authz.ApplicationsManage, m.handler.GetGrant)
	register("PUT /api/v1/admin/apps/{app_id}/staff-grants/{staff_id}", authz.ApplicationsManage, m.handler.EnableGrant)
	register("DELETE /api/v1/admin/apps/{app_id}/staff-grants/{staff_id}", authz.ApplicationsManage, m.handler.RevokeGrant)
}
