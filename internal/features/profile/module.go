package profile

import (
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/staffmutation"
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

// NewProfileModule binds profile reads and transactional, session-checked writes.
func NewProfileModule(q *sqlc.Queries, db staffmutation.Transactor, middlewares ...middleware.Middleware) *ProfileModule {
	return &ProfileModule{middlewares: middlewares, handler: NewProfileHandler(NewProfileService(q, db))}
}
