package staff

import (
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
)

// StaffModule represents the staff feature module with its dependencies.
type StaffModule struct {
	middlewares []middleware.Middleware
	handler     *StaffHandler
}

// NewStaffModule creates a new StaffModule instance with the given database queries and middlewares.
func NewStaffModule(queries *sqlc.Queries, middlewares ...middleware.Middleware) *StaffModule {
	staffRepo := repository.NewStaffUserRepository(queries)
	roleRepo := repository.NewRoleRepository(queries)
	service := NewStaffService(staffRepo, roleRepo)
	handler := NewStaffHandler(service)

	return &StaffModule{
		middlewares: middlewares,
		handler:     handler,
	}
}
