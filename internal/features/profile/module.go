package profile

import (
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
)

// ProfileModule represents the profile feature module.
//
// It is the composition root of the profile feature: it owns the HTTP
// handler, holds the ordered chain of middlewares that gate the feature's
// endpoints, and is the unit that cmd/server constructs once at startup. It
// follows the same shape as the auth, mobile, notifications, and staff
// modules, with the smallest dependency graph of any of them.
//
// # What the module owns
//
// On construction, the module builds and retains:
//
//   - A StaffUserRepository and a RoleRepository, the only two repositories
//     the profile feature needs.
//   - A ProfileService composed from the two repositories.
//   - A ProfileHandler wrapping the service.
//
// The module does not own the dependencies it receives — the sqlc.Queries
// and the middlewares belong to the caller — and it does not expose any of
// its internal repositories or the service to the caller.
//
// # What the module deliberately does not do
//
// Unlike the notifications and staff modules, the profile module does not
// wire an audit service. Profile operations are not currently recorded in
// the audit trail; only their effect on the staff_users row (through the
// UpdatedAt timestamp) is visible. If profile actions need to be auditable
// in the future — for example, to track who changed their email address —
// an audit dependency would need to be threaded through the constructor
// and the service.
//
// # Concurrency
//
// The module is safe for concurrent use. Its fields are set once in the
// constructor and never mutated; RegisterRoutes only reads them.
type ProfileModule struct {
	middlewares []middleware.Middleware
	handler     *ProfileHandler
}

// NewProfileModule creates a new ProfileModule instance.
//
// # Wiring
//
// The constructor performs the full dependency-injection chain for the
// profile feature in a single call:
//
//  1. Builds a StaffUserRepository and a RoleRepository from the supplied
//     sqlc.Queries. These are the same repository types used by the auth
//     and staff features, constructed here independently — the module does
//     not share repository instances with other modules, but the underlying
//     pgx pool is shared, so the connection cost is not duplicated.
//  2. Builds a ProfileService from the two repositories.
//  3. Builds a ProfileHandler from the service.
//  4. Returns the module with the supplied middlewares and the handler.
//
// # Parameters
//
// queries must be non-nil and is expected to be the shared instance from
// cmd/server. The middlewares parameter is variadic so that the caller can
// pass whichever cross-cutting concerns apply to the profile endpoints —
// in the current wiring the API-key and JWT middlewares — without the
// module hardcoding a chain. The order in which they are supplied is the
// order in which they are applied; see middleware.Chain for the
// composition rule.
//
// # Concurrency
//
// Intended to be called once during process startup, before the HTTP server
// begins listening.
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
