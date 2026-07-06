package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

type NotificationTargetRepository struct {
	q sqlc.Querier
}

func NewNotificationTargetRepository(q sqlc.Querier) *NotificationTargetRepository {
	return &NotificationTargetRepository{q: q}
}

func (r *NotificationTargetRepository) CountByNotification(ctx context.Context, notificationID int64) (int64, error) {
	return r.q.CountNotificationTargets(ctx, notificationID)
}

func (r *NotificationTargetRepository) Create(ctx context.Context, params sqlc.CreateNotificationTargetParams) (sqlc.CreateNotificationTargetRow, error) {
	return r.q.CreateNotificationTarget(ctx, params)
}

func (r *NotificationTargetRepository) CreateFull(ctx context.Context, params sqlc.CreateNotificationTargetFullParams) (sqlc.NotificationTarget, error) {
	return r.q.CreateNotificationTargetFull(ctx, params)
}

func (r *NotificationTargetRepository) ListByNotification(ctx context.Context, notificationID int64, offset, limit int32) ([]sqlc.ListNotificationTargetsRow, error) {
	return r.q.ListNotificationTargets(ctx, sqlc.ListNotificationTargetsParams{
		NotificationID: notificationID,
		Offset:         offset,
		Limit:          limit,
	})
}

func (r *NotificationTargetRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteNotificationTarget(ctx, id)
}

func (r *NotificationTargetRepository) DeleteByNotification(ctx context.Context, notificationID int64) error {
	return r.q.DeleteNotificationTargetsByNotification(ctx, notificationID)
}
