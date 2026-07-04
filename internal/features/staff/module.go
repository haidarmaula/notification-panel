package staff

import (
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
)

type StaffModule struct {
	middlewares []middleware.Middleware
	handler     *StaffHandler
}

func NewStaffModule(queries *sqlc.Queries, middlewares ...middleware.Middleware) *StaffModule {
	staffRepo := repository.NewStaffRepository(queries)
	roleRepo := repository.NewRoleRepository(queries)
	service := NewStaffService(staffRepo, roleRepo)
	handler := NewStaffHandler(service)

	return &StaffModule{
		middlewares: middlewares,
		handler:     handler,
	}
}
