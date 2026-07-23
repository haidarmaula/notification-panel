package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

type AuditLogRepository struct {
	q sqlc.Querier
}

func NewAuditLogRepository(q sqlc.Querier) *AuditLogRepository {
	return &AuditLogRepository{q: q}
}

func (r *AuditLogRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountAuditLogs(ctx)
}

func (r *AuditLogRepository) Create(ctx context.Context, params sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	return r.q.CreateAuditLog(ctx, params)
}

func (r *AuditLogRepository) FindByID(ctx context.Context, id int64) (sqlc.GetAuditLogByIDRow, error) {
	return r.q.GetAuditLogByID(ctx, id)
}

func (r *AuditLogRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListAuditLogsRow, error) {
	return r.q.ListAuditLogs(ctx, sqlc.ListAuditLogsParams{
		Offset: offset,
		Limit:  limit,
	})
}
