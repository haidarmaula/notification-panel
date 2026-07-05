package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

type UploadBatchRepository struct {
	q sqlc.Querier
}

func NewUploadBatchRepository(q sqlc.Querier) *UploadBatchRepository {
	return &UploadBatchRepository{q: q}
}

func (r *UploadBatchRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountUploadBatches(ctx)
}

func (r *UploadBatchRepository) FindByID(ctx context.Context, id int64) (sqlc.UploadBatch, error) {
	return r.q.GetUploadBatchByID(ctx, id)
}

func (r *UploadBatchRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListUploadBatchesRow, error) {
	return r.q.ListUploadBatches(ctx, sqlc.ListUploadBatchesParams{
		Offset: offset,
		Limit:  limit,
	})
}

func (r *UploadBatchRepository) Create(ctx context.Context, params sqlc.CreateUploadBatchParams) (sqlc.UploadBatch, error) {
	return r.q.CreateUploadBatch(ctx, params)
}
