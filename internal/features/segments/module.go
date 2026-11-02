package segments

import (
	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
)

// SegmentModule represents the segment feature module.
//
// It is the composition root of the segment feature: it owns the two
// handlers (the segment CRUD handler and the members handler), holds the
// ordered chain of middlewares that gate the feature's endpoints, and is
// the unit that cmd/server constructs once at startup. It follows the same
// shape as the auth, mobile, notifications, profile, and staff modules,
// with one difference — it exposes two handlers rather than one, because
// the members sub-resource has its own service and handler.
//
// # What the module owns
//
// On construction, the module builds and retains:
//
//   - Four repositories (segment, member, staff, user) built from the
//     shared sqlc.Queries.
//   - An audit service built on a dedicated audit-log repository.
//   - A SegmentService (built against the interface-typed constructors)
//     and a MembersService (built against concrete types).
//   - A SegmentHandler wrapping the segment service and a MembersHandler
//     wrapping the members service.
//
// The module does not own the dependencies it receives — the sqlc.Queries
// and the middlewares belong to the caller — and it does not expose any of
// its internal services or repositories to the caller.
//
// # Bridge between interface-typed and concrete-typed services
//
// The two services this module constructs have different dependency
// styles: NewSegmentService accepts interface-typed repositories, while
// NewMembersService accepts concrete repository types. The module bridges
// the two by constructing concrete repositories and passing them to both
// constructors — Go's structural interface satisfaction allows a concrete
// *repository.SegmentRepository to satisfy the SegmentRepository
// interface without an explicit conversion. The result is that the segment
// service depends on abstractions (and is testable with fakes), while the
// members service depends on concrete types (and is not).
//
// # Concurrency
//
// The module is safe for concurrent use. Its fields are set once in the
// constructor and never mutated; RegisterRoutes only reads them.
type SegmentModule struct {
	authorization  authz.Checker
	middlewares    []middleware.Middleware
	handler        *SegmentHandler
	membersHandler *MembersHandler
}

// NewSegmentModule creates a new SegmentModule instance.
//
// # Wiring
//
// The constructor performs the full dependency-injection chain for the
// segment feature in a single call:
//
//  1. Builds four repositories from the supplied sqlc.Queries: segment,
//     member, staff, and user. The staff repository is used by the segment
//     service to look up the creating user's display name; the user
//     repository is used by the members service to verify that a user
//     exists before adding them to a segment.
//  2. Builds an audit-log repository and an audit.AuditService on top of
//     it. The audit service is used by the segment service for the
//     create/update/delete actions; the members service does not currently
//     audit its operations.
//  3. Builds a SegmentService from the four collaborators and a
//     MembersService from the three it needs. The two services share the
//     segment and member repositories but are otherwise independent.
//  4. Builds a SegmentHandler and a MembersHandler from the two services.
//  5. Returns the module with the supplied middlewares and both handlers.
//
// # Parameters
//
// queries must be non-nil and is expected to be the shared instance from
// cmd/server. The middlewares parameter is variadic so that the caller can
// pass whichever cross-cutting concerns apply — in the current wiring the
// API-key, JWT, and audit middlewares — without the module hardcoding a
// chain. The order in which they are supplied is the order in which they
// are applied; see middleware.Chain for the composition rule.
//
// # Concurrency
//
// Intended to be called once during process startup, before the HTTP server
// begins listening.
func NewSegmentModule(queries *sqlc.Queries, middlewares ...middleware.Middleware) *SegmentModule {
	segmentRepo := repository.NewSegmentRepository(queries)
	memberRepo := repository.NewSegmentMemberRepository(queries)
	staffRepo := repository.NewStaffUserRepository(queries)
	authorization := authz.New(staffRepo)
	userRepo := repository.NewUserRepository(queries)

	auditRepo := repository.NewAuditLogRepository(queries)
	auditService := audit.NewAuditService(auditRepo)

	segmentService := NewSegmentService(segmentRepo, memberRepo, staffRepo, auditService, authorization)
	segmentHandler := NewSegmentHandler(segmentService)

	membersService := NewMembersService(segmentRepo, memberRepo, userRepo, authorization)
	membersHandler := NewMembersHandler(membersService)

	return &SegmentModule{
		authorization:  authorization,
		middlewares:    middlewares,
		handler:        segmentHandler,
		membersHandler: membersHandler,
	}
}
