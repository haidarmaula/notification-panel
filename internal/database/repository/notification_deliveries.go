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
//
// # User-facing vs admin-facing projections
//
// Two of the read methods — ListByNotification and ListByUser — are
// distinguished not only by filter but by the granularity of the result:
//
//   - ListByNotification returns one row per delivery, which is the correct
//     projection for the admin portal's per-campaign delivery listing.
//   - ListByUser returns one row per *notification*, collapsing the
//     deliveries that belong to a multi-device user into a single
//     representative row. That is the projection the mobile inbox expects,
//     where the same notification is shown once regardless of how many
//     devices received it.
//
// The asymmetry is deliberate. Callers that need per-delivery detail for a
// user should query the table directly or use ListByNotification scoped to
// the relevant notification ID.
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
// That is the intended semantics for a per-campaign delivery rate: each
// attempted push is a distinct unit of work, and a user with two devices
// legitimately represents two delivery attempts.
func (r *NotificationDeliveryRepository) CountByNotification(ctx context.Context, notificationID int64) (int64, error) {
	return r.q.CountNotificationDeliveries(ctx, notificationID)
}

// CountByUser returns the total number of distinct notifications a user has
// received.
//
// The count is of distinct notification_id values, not of delivery rows, so
// it matches the number of items that ListByUser can return for the same
// user. A user with three devices who has received ten notifications
// reports ten, not thirty.
//
// This is the total the mobile inbox uses for its "X notifications"
// pagination metadata; it would be wrong to use the raw delivery count for
// that purpose, because the client would then believe there are more
// notifications than it can ever page to.
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
//
// The result is one row per delivery, not one row per notification or per
// user: a notification delivered to a user with three registered devices
// produces three rows. That is the correct granularity for the admin view,
// which is interested in per-device delivery success and failure. Callers
// that need the collapsed per-notification view for a single user should use
// ListByUser instead.
func (r *NotificationDeliveryRepository) ListByNotification(ctx context.Context, notificationID int64, offset, limit int32) ([]sqlc.ListNotificationDeliveriesRow, error) {
	return r.q.ListNotificationDeliveries(ctx, sqlc.ListNotificationDeliveriesParams{
		NotificationID: notificationID,
		Offset:         offset,
		Limit:          limit,
	})
}

// ListByUser returns a page of distinct notifications for a specific user,
// joined with the parent notification so the caller can render the mobile
// app's inbox without a second query.
//
// offset and limit are passed through to SQL without validation.
//
// # One row per notification, not per delivery
//
// The underlying query collapses the notification_deliveries rows that
// belong to a multi-device user into a single representative row per
// notification, so a user with three devices who has received ten
// notifications sees ten entries in their inbox, not thirty.
//
// The representative delivery is chosen by a status priority of
//
//	OPENED > DELIVERED > SENT > FAILED > (anything else)
//
// so a notification that the user has opened on any device is reported as
// OPENED, even if another device's delivery is still in DELIVERED or FAILED.
// The rationale is that the user's perception of "have I read this?" is the
// most meaningful state for an inbox, and it should not be downgraded by the
// failure or lag of a secondary device. Ordering is by the representative
// delivery's created_at descending, so the most recently received
// notification appears first.
//
// The row shape (sqlc.ListUserNotificationsRow) is unchanged by this
// collapsing: the Status field carries the priority-selected status, and the
// OpenedAt field carries the opened_at of whichever delivery produced that
// status (or NULL when no delivery has been opened).
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
