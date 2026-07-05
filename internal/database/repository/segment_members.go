package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

type SegmentMemberRepository struct {
	q sqlc.Querier
}

func NewSegmentMemberRepository(q sqlc.Querier) *SegmentMemberRepository {
	return &SegmentMemberRepository{q: q}
}

func (r *SegmentMemberRepository) CountBySegment(ctx context.Context, segmentID int64) (int64, error) {
	return r.q.CountSegmentMembers(ctx, segmentID)
}

func (r *SegmentMemberRepository) FindByID(ctx context.Context, id int64) (sqlc.SegmentMember, error) {
	return r.q.GetSegmentMemberByID(ctx, id)
}

func (r *SegmentMemberRepository) ListBySegment(ctx context.Context, segmentID int64, offset, limit int32) ([]sqlc.ListSegmentMembersRow, error) {
	return r.q.ListSegmentMembers(ctx, sqlc.ListSegmentMembersParams{
		SegmentID: segmentID,
		Offset:    offset,
		Limit:     limit,
	})
}

func (r *SegmentMemberRepository) Create(ctx context.Context, params sqlc.CreateSegmentMemberParams) (sqlc.SegmentMember, error) {
	return r.q.CreateSegmentMember(ctx, params)
}

func (r *SegmentMemberRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteSegmentMember(ctx, id)
}
