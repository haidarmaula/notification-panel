package staff

import (
	"hello/internal/authz"
	"hello/internal/staffmutation"

	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
)

// StaffModule represents the staff feature module with its dependencies.
//
// It is the composition root of the staff feature: it owns the HTTP
// handler, holds the ordered chain of middlewares that gate the feature's
// endpoints, and is the unit that cmd/server constructs once at startup.
// It follows the same shape as the auth, mobile, notifications, profile,
// and segments modules, with one structural property worth noting — the
// module is the only one in the codebase whose handler depends on an
// interface (StaffServicer) rather than a concrete service type. The
// module bridges the two by passing the concrete *StaffService it
// constructs; the interface is satisfied structurally.
//
// # What the module owns
//
// On construction, the module builds and retains:
//
//   - Three repositories (staff, role, audit) built from the shared
//     sqlc.Queries.
//   - An audit service built on the audit-log repository.
//   - A StaffService (built against interface-typed dependencies).
//   - A StaffHandler wrapping the concrete service through the
//     StaffServicer interface.
//
// The module does not own the dependencies it receives — the sqlc.Queries
// and the middlewares belong to the caller — and it does not expose any of
// its internal repositories or the service to the caller.
//
// # Concurrency
//
// The module is safe for concurrent use. Its fields are set once in the
// constructor and never mutated; RegisterRoutes only reads them.
type StaffModule struct {
	middlewares []middleware.Middleware
	handler     *StaffHandler
	permissions authz.Checker
}

// NewStaffModule binds read operations to the shared pool and each mutation
// to a transaction that locks the authenticated actor and target until commit.
func NewStaffModule(queries *sqlc.Queries, db staffmutation.Transactor, middlewares ...middleware.Middleware) *StaffModule {
	service := NewStaffService(queries, db, authz.New(repository.NewStaffUserRepository(queries)))
	return &StaffModule{middlewares: middlewares, handler: NewStaffHandler(service), permissions: service.permissions}
}
