package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

type NotificationDeliveryRepository struct {
	q sqlc.Querier
}

func NewNotificationDeliveryRepository(q sqlc.Querier) *NotificationDeliveryRepository {
	return &NotificationDeliveryRepository{q: q}
}

func (r *NotificationDeliveryRepository) CountByNotification(ctx context.Context, notificationID int64) (int64, error) {
	return r.q.CountNotificationDeliveries(ctx, notificationID)
}

// CountByUser returns the total number of notification deliveries for a user.
func (r *NotificationDeliveryRepository) CountByUser(ctx context.Context, userID int64) (int64, error) {
	return r.q.CountUserNotifications(ctx, userID)
}

func (r *NotificationDeliveryRepository) Create(ctx context.Context, params sqlc.CreateNotificationDeliveryParams) (sqlc.NotificationDelivery, error) {
	return r.q.CreateNotificationDelivery(ctx, params)
}

func (r *NotificationDeliveryRepository) FindByID(ctx context.Context, id int64) (sqlc.GetNotificationDeliveryByIDRow, error) {
	return r.q.GetNotificationDeliveryByID(ctx, id)
}

func (r *NotificationDeliveryRepository) ListByNotification(ctx context.Context, notificationID int64, offset, limit int32) ([]sqlc.ListNotificationDeliveriesRow, error) {
	return r.q.ListNotificationDeliveries(ctx, sqlc.ListNotificationDeliveriesParams{
		NotificationID: notificationID,
		Offset:         offset,
		Limit:          limit,
	})
}

// ListByUser returns notification deliveries for a specific user (joined with notifications).
func (r *NotificationDeliveryRepository) ListByUser(ctx context.Context, userID int64, offset, limit int32) ([]sqlc.ListUserNotificationsRow, error) {
	return r.q.ListUserNotifications(ctx, sqlc.ListUserNotificationsParams{
		UserID: userID,
		Offset: offset,
		Limit:  limit,
	})
}

func (r *NotificationDeliveryRepository) MarkDelivered(ctx context.Context, id int64) error {
	return r.q.MarkNotificationDelivered(ctx, id)
}

func (r *NotificationDeliveryRepository) MarkFailed(ctx context.Context, params sqlc.MarkNotificationFailedParams) error {
	return r.q.MarkNotificationFailed(ctx, params)
}

func (r *NotificationDeliveryRepository) MarkOpened(ctx context.Context, id int64) error {
	return r.q.MarkNotificationOpened(ctx, id)
}
