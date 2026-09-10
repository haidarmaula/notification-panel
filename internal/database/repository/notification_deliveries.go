package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

// NotificationDeliveryRepository provides read and write access to the
// notification_deliveries table.
//
// A delivery row represents the attempt to push one notification to one
// device token. The lifecycle of a row is: created by the worker when it
// begins fan-out, then transitioned to delivered, failed, or opened. The
// repository exposes those transitions as intent-named methods rather than
// generic Update calls, so call sites read as state machines rather than as
// column assignments.
//
// The repository does not enforce ordering or idempotency between
// transitions: for example, calling MarkDelivered twice is not rejected here.
// Whether a transition is a no-op or an error when applied to a row that is
// already in the target state is a property of the SQL query, not this
// wrapper.
type NotificationDeliveryRepository struct {
	q sqlc.Querier
}

// NewNotificationDeliveryRepository constructs a
// NotificationDeliveryRepository bound to q.
//
// q must be non-nil. Typical callers pass sqlc.New(db) from the worker's
// main; test fakes and transaction-scoped queriers are equally valid.
func NewNotificationDeliveryRepository(q sqlc.Querier) *NotificationDeliveryRepository {
	return &NotificationDeliveryRepository{q: q}
}

// CountByNotification returns the total number of delivery attempts for a
// single notification.
//
// This is the denominator of the per-campaign delivery rate displayed in the
// admin analytics view. It counts rows, not distinct users or devices — if a
// user has multiple active tokens, each token contributes independently.
func (r *NotificationDeliveryRepository) CountByNotification(ctx context.Context, notificationID int64) (int64, error) {
	return r.q.CountNotificationDeliveries(ctx, notificationID)
}

// CountByUser returns the total number of notification deliveries for a user.
//
// "Deliveries" here means rows in notification_deliveries — one per device
// per notification — so a user with three devices who has received ten
// notifications will report thirty. Callers that need per-notification
// counts should join against notifications or use ListByUser, which returns
// the joined row shape.
func (r *NotificationDeliveryRepository) CountByUser(ctx context.Context, userID int64) (int64, error) {
	return r.q.CountUserNotifications(ctx, userID)
}

// Create inserts a new notification delivery row and returns the persisted
// record.
//
// It is called by the worker once per (notification, device token) pair,
// typically in a batch during fan-out. If the underlying table has a unique
// constraint on (notification_id, device_token_id) — which is the intended
// design for at-least-once Kafka semantics — a duplicate insert will surface
// as a pgx error. Callers that need idempotent fan-out should either
// tolerate that error or route through an upsert query instead.
func (r *NotificationDeliveryRepository) Create(ctx context.Context, params sqlc.CreateNotificationDeliveryParams) (sqlc.NotificationDelivery, error) {
	return r.q.CreateNotificationDelivery(ctx, params)
}

// FindByID retrieves a single delivery by its primary key.
//
// The return type is sqlc.GetNotificationDeliveryByIDRow, which joins in
// related context (such as the notification title or the user identity)
// beyond the raw table columns. pgx.ErrNoRows is returned unchanged when no
// row matches.
func (r *NotificationDeliveryRepository) FindByID(ctx context.Context, id int64) (sqlc.GetNotificationDeliveryByIDRow, error) {
	return r.q.GetNotificationDeliveryByID(ctx, id)
}

// ListByNotification returns a page of delivery rows for a single
// notification, ordered by the query's default sort.
//
// offset and limit are passed through to SQL without validation. The row
// type carries joined context (user identity, device metadata), making this
// the natural endpoint for the admin portal's per-campaign delivery listing.
func (r *NotificationDeliveryRepository) ListByNotification(ctx context.Context, notificationID int64, offset, limit int32) ([]sqlc.ListNotificationDeliveriesRow, error) {
	return r.q.ListNotificationDeliveries(ctx, sqlc.ListNotificationDeliveriesParams{
		NotificationID: notificationID,
		Offset:         offset,
		Limit:          limit,
	})
}

// ListByUser returns notification deliveries for a specific user, joined with
// the parent notification so the caller can render the mobile app's inbox
// without a second query.
//
// offset and limit are passed through to SQL without validation. The row
// type exposes the joined shape and is not interchangeable with
// sqlc.NotificationDelivery.
func (r *NotificationDeliveryRepository) ListByUser(ctx context.Context, userID int64, offset, limit int32) ([]sqlc.ListUserNotificationsRow, error) {
	return r.q.ListUserNotifications(ctx, sqlc.ListUserNotificationsParams{
		UserID: userID,
		Offset: offset,
		Limit:  limit,
	})
}

// MarkDelivered transitions a delivery row to its delivered state.
//
// It is called by the worker after the push provider has accepted the
// message for delivery, and is intended to be a one-way transition out of
// the initial "pending" state. The method does not read the row first; the
// SQL query is responsible for deciding whether to guard on the current
// state. Callers must not assume the operation is idempotent — a second
// invocation may either be a harmless no-op or an error depending on the
// query's WHERE clause.
func (r *NotificationDeliveryRepository) MarkDelivered(ctx context.Context, id int64) error {
	return r.q.MarkNotificationDelivered(ctx, id)
}

// MarkFailed transitions a delivery row to its failed state and records the
// reason.
//
// params carries the delivery ID and the failure description — typically a
// provider error code or a truncated error message. As with MarkDelivered,
// the repository does not enforce a particular source state; the query
// decides whether the transition is legal.
func (r *NotificationDeliveryRepository) MarkFailed(ctx context.Context, params sqlc.MarkNotificationFailedParams) error {
	return r.q.MarkNotificationFailed(ctx, params)
}

// MarkOpened transitions a delivery row to its opened state.
//
// This is triggered by a client-reported event — the user tapped the
// notification in the mobile app — rather than by the push provider. As with
// the other Mark* methods, the repository does not enforce state-transition
// validity; callers that need to reject out-of-order events (for example, an
// "opened" event arriving before "delivered") must guard at the service
// layer or in the SQL.
func (r *NotificationDeliveryRepository) MarkOpened(ctx context.Context, id int64) error {
	return r.q.MarkNotificationOpened(ctx, id)
}
