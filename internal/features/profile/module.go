package profile

import (
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
)

// ProfileModule represents the profile feature module.
type ProfileModule struct {
	middlewares []middleware.Middleware
	handler     *ProfileHandler
}

// NewProfileModule creates a new ProfileModule instance.
func NewProfileModule(queries *sqlc.Queries, middlewares ...middleware.Middleware) *ProfileModule {
	staffRepo := repository.NewStaffUserRepository(queries)
	roleRepo := repository.NewRoleRepository(queries)
	service := NewProfileService(staffRepo, roleRepo)
	handler := NewProfileHandler(service)

	return &ProfileModule{
		middlewares: middlewares,
		handler:     handler,
	}
}
