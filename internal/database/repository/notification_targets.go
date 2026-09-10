package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

// NotificationTargetRepository provides read and write access to the
// notification_targets table.
//
// A target row records one strategy by which a notification should reach its
// audience: for example, "segment X" or "explicit user list [1,2,3]". A
// single notification may have multiple target rows if the admin portal
// supports multi-strategy targeting; the worker's fan-out stage resolves
// each target independently and unions the resulting device tokens.
//
// The repository is intentionally narrow. Target rows are created together
// with their parent notification and deleted wholesale when that
// notification is removed; there is no general-purpose Update method, on the
// assumption that editing a target in place is not a supported workflow.
type NotificationTargetRepository struct {
	q sqlc.Querier
}

// NewNotificationTargetRepository constructs a NotificationTargetRepository
// bound to q.
//
// q must be non-nil. Typical callers pass sqlc.New(db) from the worker or the
// API server; a transaction-scoped querier is equally valid when creating a
// notification and its targets must be committed atomically.
func NewNotificationTargetRepository(q sqlc.Querier) *NotificationTargetRepository {
	return &NotificationTargetRepository{q: q}
}

// CountByNotification returns the number of target strategies attached to a
// single notification.
//
// This is not an audience size — it is the count of target rows, which is
// typically 1 but may be higher if the notification targets multiple
// segments. Callers that need audience size must resolve each target and
// sum the resulting device token or user counts.
func (r *NotificationTargetRepository) CountByNotification(ctx context.Context, notificationID int64) (int64, error) {
	return r.q.CountNotificationTargets(ctx, notificationID)
}

// Create inserts a notification target and returns the sqlc-generated
// CreateNotificationTargetRow projection.
//
// This variant returns a narrower row than CreateFull. It exists because the
// admin portal's "add target to existing notification" endpoint only needs
// the new row's identity and a small subset of columns, and the narrower
// projection keeps that response lean. Use CreateFull when the caller needs
// the complete target model.
func (r *NotificationTargetRepository) Create(ctx context.Context, params sqlc.CreateNotificationTargetParams) (sqlc.CreateNotificationTargetRow, error) {
	return r.q.CreateNotificationTarget(ctx, params)
}

// CreateFull inserts a notification target and returns the complete
// sqlc.NotificationTarget model.
//
// Prefer this variant when the caller intends to hand the resulting value to
// a domain layer that expects a fully-populated target — for example, when a
// notification and its targets are created in one request and the response
// echoes the created entities. As with Create, the underlying query performs
// no conflict handling; if the schema enforces a uniqueness constraint on
// (notification_id, strategy) a duplicate will surface as a pgx error.
func (r *NotificationTargetRepository) CreateFull(ctx context.Context, params sqlc.CreateNotificationTargetFullParams) (sqlc.NotificationTarget, error) {
	return r.q.CreateNotificationTargetFull(ctx, params)
}

// ListByNotification returns a page of target rows for a single notification.
//
// offset and limit are passed through to SQL without validation, following
// the convention used elsewhere in this package. Because notifications
// rarely have more than a handful of targets, this method is often called
// with a generous limit rather than paginated.
func (r *NotificationTargetRepository) ListByNotification(ctx context.Context, notificationID int64, offset, limit int32) ([]sqlc.ListNotificationTargetsRow, error) {
	return r.q.ListNotificationTargets(ctx, sqlc.ListNotificationTargetsParams{
		NotificationID: notificationID,
		Offset:         offset,
		Limit:          limit,
	})
}

// Delete removes a single target row by its primary key.
//
// Deleting a non-existent row is not an error; the underlying DELETE is a
// no-op in that case. This method is appropriate when the admin removes one
// target from a multi-target notification; use DeleteByNotification to
// remove them all at once.
func (r *NotificationTargetRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteNotificationTarget(ctx, id)
}

// DeleteByNotification removes every target row attached to a notification.
//
// It is the cleanup half of a notification delete: the target rows have no
// meaning without their parent, and keeping them would leave orphaned
// references in the fan-out pipeline. The method performs a single bulk
// DELETE and is not transactional with the parent notification delete
// unless the caller wraps both in a shared transaction.
func (r *NotificationTargetRepository) DeleteByNotification(ctx context.Context, notificationID int64) error {
	return r.q.DeleteNotificationTargetsByNotification(ctx, notificationID)
}
