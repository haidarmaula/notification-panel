package segments

import (
	"hello/internal/audit"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
)

// SegmentModule represents the segment feature module.
type SegmentModule struct {
	middlewares    []middleware.Middleware
	handler        *SegmentHandler
	membersHandler *MembersHandler
}

// NewSegmentModule creates a new SegmentModule instance.
func NewSegmentModule(queries *sqlc.Queries, middlewares ...middleware.Middleware) *SegmentModule {
	segmentRepo := repository.NewSegmentRepository(queries)
	memberRepo := repository.NewSegmentMemberRepository(queries)
	staffRepo := repository.NewStaffUserRepository(queries)
	userRepo := repository.NewUserRepository(queries)

	auditRepo := repository.NewAuditLogRepository(queries)
	auditService := audit.NewAuditService(auditRepo)

	segmentService := NewSegmentService(segmentRepo, memberRepo, staffRepo, auditService)
	segmentHandler := NewSegmentHandler(segmentService)

	membersService := NewMembersService(segmentRepo, memberRepo, userRepo)
	membersHandler := NewMembersHandler(membersService)

	return &SegmentModule{
		middlewares:    middlewares,
		handler:        segmentHandler,
		membersHandler: membersHandler,
	}
}
