package repository

import (
	"context"

	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

type SegmentRepository struct {
	q sqlc.Querier
}

func NewSegmentRepository(q sqlc.Querier) *SegmentRepository {
	return &SegmentRepository{q: q}
}

func (r *SegmentRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountSegments(ctx)
}

func (r *SegmentRepository) FindByID(ctx context.Context, id int64) (sqlc.Segment, error) {
	return r.q.GetSegmentByID(ctx, id)
}

func (r *SegmentRepository) FindByName(ctx context.Context, name string) (sqlc.Segment, error) {
	return r.q.GetSegmentByName(ctx, name)
}

func (r *SegmentRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListSegmentsRow, error) {
	return r.q.ListSegments(ctx, sqlc.ListSegmentsParams{
		Offset: offset,
		Limit:  limit,
	})
}

func (r *SegmentRepository) Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchSegmentsRow, error) {
	return r.q.SearchSegments(ctx, sqlc.SearchSegmentsParams{
		Keyword: pgtype.Text{String: keyword, Valid: true},
		Offset:  offset,
		Limit:   limit,
	})
}

func (r *SegmentRepository) Create(ctx context.Context, params sqlc.CreateSegmentParams) (sqlc.Segment, error) {
	return r.q.CreateSegment(ctx, params)
}

func (r *SegmentRepository) Update(ctx context.Context, params sqlc.UpdateSegmentParams) error {
	return r.q.UpdateSegment(ctx, params)
}

func (r *SegmentRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteSegment(ctx, id)
}

func (r *SegmentRepository) ExistsByName(ctx context.Context, name string) (bool, error) {
	return r.q.ExistsSegmentByName(ctx, name)
}
