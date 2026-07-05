package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

type UploadBatchRowRepository struct {
	q sqlc.Querier
}

func NewUploadBatchRowRepository(q sqlc.Querier) *UploadBatchRowRepository {
	return &UploadBatchRowRepository{q: q}
}

func (r *UploadBatchRowRepository) CountByBatch(ctx context.Context, batchID int64) (int64, error) {
	return r.q.CountUploadBatchRows(ctx, batchID)
}

func (r *UploadBatchRowRepository) ListByBatch(ctx context.Context, batchID int64, offset, limit int32) ([]sqlc.UploadBatchRow, error) {
	return r.q.ListUploadBatchRows(ctx, sqlc.ListUploadBatchRowsParams{
		BatchID: batchID,
		Offset:  offset,
		Limit:   limit,
	})
}

func (r *UploadBatchRowRepository) Create(ctx context.Context, params sqlc.CreateUploadBatchRowParams) (sqlc.UploadBatchRow, error) {
	return r.q.CreateUploadBatchRow(ctx, params)
}
