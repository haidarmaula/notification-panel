package repository

import (
	"context"

	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

type NotificationRepository struct {
	q sqlc.Querier
}

func NewNotificationRepository(q sqlc.Querier) *NotificationRepository {
	return &NotificationRepository{q: q}
}

func (r *NotificationRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountNotifications(ctx)
}

func (r *NotificationRepository) Create(ctx context.Context, params sqlc.CreateNotificationParams) (sqlc.CreateNotificationRow, error) {
	return r.q.CreateNotification(ctx, params)
}

func (r *NotificationRepository) FindByID(ctx context.Context, id int64) (sqlc.GetNotificationByIDRow, error) {
	return r.q.GetNotificationByID(ctx, id)
}

func (r *NotificationRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListNotificationsRow, error) {
	return r.q.ListNotifications(ctx, sqlc.ListNotificationsParams{
		Offset: offset,
		Limit:  limit,
	})
}

func (r *NotificationRepository) Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchNotificationsRow, error) {
	return r.q.SearchNotifications(ctx, sqlc.SearchNotificationsParams{
		Keyword: pgtype.Text{String: keyword, Valid: true},
		Offset:  offset,
		Limit:   limit,
	})
}

func (r *NotificationRepository) Update(ctx context.Context, params sqlc.UpdateNotificationParams) error {
	return r.q.UpdateNotification(ctx, params)
}

func (r *NotificationRepository) UpdateStatus(ctx context.Context, params sqlc.UpdateNotificationStatusParams) error {
	return r.q.UpdateNotificationStatus(ctx, params)
}

func (r *NotificationRepository) MarkSent(ctx context.Context, id int64) error {
	return r.q.MarkNotificationSent(ctx, id)
}

func (r *NotificationRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteNotification(ctx, id)
}
