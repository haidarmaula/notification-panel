package applications

import (
	"hello/internal/authz"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"net/http"
)

// RegisterOnboardingSetup mounts only implemented setup operations when their
// protected startup dependencies are configured. Lifecycle/release execution is
// deliberately not exposed by the setup runtime's database identity.
func RegisterOnboardingSetup(mux *http.ServeMux, q sqlc.Querier, service *OnboardingService, middlewares ...middleware.Middleware) {
	if service == nil {
		return
	}
	if mux == nil || q == nil {
		panic("applications: onboarding route dependencies required")
	}
	h := NewOnboardingHandler(service)
	permissions := authz.New(repository.NewStaffUserRepository(q))
	use := middleware.Chain(middlewares...)
	wrap := func(handler http.HandlerFunc) http.Handler {
		guarded := use(authz.Guard(permissions, authz.ApplicationsManage, handler))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			guarded.ServeHTTP(w, r)
		})
	}
	const prefix = "/api/v1/admin/apps/{app_id}/onboarding"
	mux.Handle("GET "+prefix, wrap(h.Readiness))
	mux.Handle("GET "+prefix+"/enrollment-profiles", wrap(h.Profiles))
	mux.Handle("PUT "+prefix+"/enrollment", wrap(h.Enrollment))
	mux.Handle("PUT "+prefix+"/fcm", wrap(h.FCM))
	mux.Handle("POST "+prefix+"/fcm/verify", wrap(h.Verify))
	mux.Handle("GET "+prefix+"/operations/{operation_id}", wrap(h.Operation))
	mux.Handle("GET "+prefix+"/approvals", wrap(h.Approvals))
	mux.Handle("POST "+prefix+"/dispatch/hold", wrap(h.Hold))
	mux.Handle("POST "+prefix+"/dispatch/release", wrap(h.Release))
	mux.Handle("POST "+prefix+"/activate", wrap(h.Activate))
	mux.Handle("GET "+prefix+"/test-installations", wrap(service.TestInstallationsHTTP))
	mux.Handle("PUT "+prefix+"/test-cohort", wrap(h.TestCohort))
	mux.Handle("GET "+prefix+"/operations/{operation_id}/targets", wrap(service.CohortTargetsHTTP))
}
