package segments

import (
	"context"
	"hello/internal/audit"
	"hello/internal/database/sqlc"
)

type SegmentRepository interface {
	Count(ctx context.Context) (int64, error)
	FindByID(ctx context.Context, id int64) (sqlc.Segment, error)
	FindByName(ctx context.Context, name string) (sqlc.Segment, error)
	List(ctx context.Context, offset, limit int32) ([]sqlc.ListSegmentsRow, error)
	Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchSegmentsRow, error)
	Create(ctx context.Context, params sqlc.CreateSegmentParams) (sqlc.Segment, error)
	Update(ctx context.Context, params sqlc.UpdateSegmentParams) error
	Delete(ctx context.Context, id int64) error
	ExistsByName(ctx context.Context, name string) (bool, error)
}

type SegmentMemberRepository interface {
	CountBySegment(ctx context.Context, segmentID int64) (int64, error)
	CountSegmentsByUser(ctx context.Context, userID int64) (int64, error)
	FindByID(ctx context.Context, id int64) (sqlc.SegmentMember, error)
	ListBySegment(ctx context.Context, segmentID int64, offset, limit int32) ([]sqlc.ListSegmentMembersRow, error)
	ListSegmentsByUser(ctx context.Context, userID int64, offset, limit int32) ([]sqlc.ListSegmentMembersByUserRow, error)
	Create(ctx context.Context, params sqlc.CreateSegmentMemberParams) (sqlc.SegmentMember, error)
	Delete(ctx context.Context, id int64) error
	DeleteBySegmentAndUser(ctx context.Context, segmentID, userID int64) error
}

type StaffUserRepository interface {
	Count(ctx context.Context) (int64, error)
	FindByID(ctx context.Context, id int64) (sqlc.StaffUser, error)
	FindByEmail(ctx context.Context, email string) (sqlc.StaffUser, error)
	List(ctx context.Context, offset, limit int32) ([]sqlc.ListStaffUsersRow, error)
	Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchStaffUsersRow, error)
	Create(ctx context.Context, params sqlc.CreateStaffUserParams) (sqlc.StaffUser, error)
	Update(ctx context.Context, params sqlc.UpdateStaffUserParams) error
	UpdatePassword(ctx context.Context, params sqlc.UpdateStaffPasswordParams) error
	UpdateStatus(ctx context.Context, params sqlc.UpdateStaffStatusParams) error
	Delete(ctx context.Context, id int64) error
	ExistsByEmail(ctx context.Context, email string) (bool, error)
}

type AuditService interface {
	Log(ctx context.Context, params audit.LogParams) error
}
