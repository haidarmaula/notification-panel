package staff

import (
	"hello/internal/audit"
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
}

// NewStaffModule creates a new StaffModule instance with the given database queries and middlewares.
//
// # Wiring
//
// The constructor performs the full dependency-injection chain for the
// staff feature in a single call:
//
//  1. Builds three repositories from the supplied sqlc.Queries: staff,
//     role, and audit-log. The staff repository backs all staff_users
//     access; the role repository resolves role names and IDs for the
//     service; the audit-log repository backs the audit service.
//  2. Builds an audit.AuditService on the audit-log repository.
//  3. Builds a StaffService from the three collaborators. The
//     constructor accepts interface-typed parameters, and the concrete
//     repository types passed here satisfy those interfaces structurally.
//  4. Builds a StaffHandler from the service. The handler's constructor
//     accepts a StaffServicer, which the concrete *StaffService satisfies.
//  5. Returns the module with the supplied middlewares and the handler.
//
// # Parameters
//
// queries must be non-nil and is expected to be the shared instance from
// cmd/server. The middlewares parameter is variadic so that the caller can
// pass whichever cross-cutting concerns apply to the staff endpoints — in
// the current wiring the API-key, JWT, super-admin, and audit middlewares
// — without the module hardcoding a chain. The order in which they are
// supplied is the order in which they are applied; see middleware.Chain
// for the composition rule.
//
// # Concurrency
//
// Intended to be called once during process startup, before the HTTP
// server begins listening.
func NewStaffModule(queries *sqlc.Queries, middlewares ...middleware.Middleware) *StaffModule {
	staffRepo := repository.NewStaffUserRepository(queries)
	roleRepo := repository.NewRoleRepository(queries)
	auditRepo := repository.NewAuditLogRepository(queries)

	auditService := audit.NewAuditService(auditRepo)
	staffService := NewStaffService(staffRepo, roleRepo, auditService)

	handler := NewStaffHandler(staffService)

	return &StaffModule{
		middlewares: middlewares,
		handler:     handler,
	}
}
