package applications

import (
	"hello/internal/authz"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"net/http"
)

// Module owns registry and project-scoped staff routes.
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
	register("GET /api/v1/apps/{app_id}/users", authz.UsersRead, m.handler.ListUsers)
	register("GET /api/v1/apps/{app_id}/users/{user_id}", authz.UsersRead, m.handler.GetUser)
	register("GET /api/v1/apps/{app_id}/users/{user_id}/devices", authz.UsersRead, m.handler.ListUserDevices)
	register("GET /api/v1/apps/{app_id}/segments", authz.SegmentsRead, m.handler.ListSegments)
	register("GET /api/v1/apps/{app_id}/segments/{segment_id}", authz.SegmentsRead, m.handler.GetSegment)
	register("GET /api/v1/apps/{app_id}/segments/{segment_id}/members", authz.MembersRead, m.handler.ListSegmentMembers)

	register("POST /api/v1/apps/{app_id}/segments", authz.SegmentsWrite, m.handler.WriteSegment)
	register("PUT /api/v1/apps/{app_id}/segments/{segment_id}", authz.SegmentsWrite, m.handler.WriteSegment)
	register("DELETE /api/v1/apps/{app_id}/segments/{segment_id}", authz.SegmentsWrite, m.handler.WriteSegment)
	register("PUT /api/v1/apps/{app_id}/segments/{segment_id}/members/{user_id}", authz.MembersWrite, m.handler.WriteSegmentMember)
	register("DELETE /api/v1/apps/{app_id}/segments/{segment_id}/members/{user_id}", authz.MembersWrite, m.handler.WriteSegmentMember)

	register("GET /api/v1/admin/apps", authz.ApplicationsManage, m.handler.ListManaged)
	register("POST /api/v1/admin/apps", authz.ApplicationsManage, m.handler.Create)
	register("GET /api/v1/admin/apps/{app_id}", authz.ApplicationsManage, m.handler.GetManaged)
	register("PATCH /api/v1/admin/apps/{app_id}", authz.ApplicationsManage, m.handler.Rename)
	register("GET /api/v1/admin/apps/{app_id}/staff-grants", authz.ApplicationsManage, m.handler.ListGrants)
	register("GET /api/v1/admin/apps/{app_id}/staff-grants/{staff_id}", authz.ApplicationsManage, m.handler.GetGrant)
	register("PUT /api/v1/admin/apps/{app_id}/staff-grants/{staff_id}", authz.ApplicationsManage, m.handler.EnableGrant)
	register("DELETE /api/v1/admin/apps/{app_id}/staff-grants/{staff_id}", authz.ApplicationsManage, m.handler.RevokeGrant)
}
