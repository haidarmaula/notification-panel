package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

type NotificationReadRepository struct {
	q sqlc.Querier
}

func NewNotificationReadRepository(q sqlc.Querier) *NotificationReadRepository {
	return &NotificationReadRepository{q: q}
}

func (r *NotificationReadRepository) CountByNotification(ctx context.Context, notificationID int64) (int64, error) {
	return r.q.CountNotificationReads(ctx, notificationID)
}

func (r *NotificationReadRepository) Create(ctx context.Context, params sqlc.CreateNotificationReadParams) (sqlc.NotificationRead, error) {
	return r.q.CreateNotificationRead(ctx, params)
}

func (r *NotificationReadRepository) Exists(ctx context.Context, notificationID, userID int64) (bool, error) {
	return r.q.ExistsNotificationRead(ctx, sqlc.ExistsNotificationReadParams{
		NotificationID: notificationID,
		UserID:         userID,
	})
}

func (r *NotificationReadRepository) ListByNotification(ctx context.Context, notificationID int64, offset, limit int32) ([]sqlc.ListNotificationReadsRow, error) {
	return r.q.ListNotificationReads(ctx, sqlc.ListNotificationReadsParams{
		NotificationID: notificationID,
		Offset:         offset,
		Limit:          limit,
	})
}
