package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

// NotificationReadRepository provides read and write access to the
// notification_reads table.
//
// A read row records the fact that a specific user has acknowledged seeing a
// specific notification in the mobile app's inbox — a distinct concept from
// a delivery (which reflects the push provider's outcome) and from an open
// (which reflects a tap on the OS-level notification banner). A user may
// read a notification without ever opening it, and may open it without ever
// reading it; the two tables are maintained independently by design.
type NotificationReadRepository struct {
	q sqlc.Querier
}

// NewNotificationReadRepository constructs a NotificationReadRepository bound
// to q.
//
// q must be non-nil. Typical callers pass sqlc.New(db) or a
// transaction-scoped querier when the read event must be committed together
// with other writes.
func NewNotificationReadRepository(q sqlc.Querier) *NotificationReadRepository {
	return &NotificationReadRepository{q: q}
}

// CountByNotification returns the number of distinct users who have marked a
// notification as read.
//
// This is the numerator of the read-rate metric shown in the admin analytics
// view. Because the table is keyed on (notification_id, user_id) and
// Create uses an upsert or a unique constraint, the count reflects users,
// not read events.
func (r *NotificationReadRepository) CountByNotification(ctx context.Context, notificationID int64) (int64, error) {
	return r.q.CountNotificationReads(ctx, notificationID)
}

// Create records that a user has read a notification.
//
// The mobile app calls this when the user opens the notification in the
// in-app inbox. Because the same user may open the same notification more
// than once — for example, on a re-launch — the underlying query is expected
// to be an upsert or to have a unique constraint on
// (notification_id, user_id) that lets duplicates be ignored. The
// repository does not itself enforce idempotency; consult
// db/queries/notification_reads.sql for the exact behaviour, and use Exists
// first if the caller needs to avoid even attempting a duplicate insert.
func (r *NotificationReadRepository) Create(ctx context.Context, params sqlc.CreateNotificationReadParams) (sqlc.NotificationRead, error) {
	return r.q.CreateNotificationRead(ctx, params)
}

// Exists reports whether a given (notification, user) pair has already been
// recorded as read.
//
// It is the cheap pre-check for idempotent read recording: a caller that
// wants to avoid triggering duplicate-key errors on Create can call Exists
// first. The method performs a single indexed lookup and returns false, nil
// when no row is found — pgx.ErrNoRows is swallowed by the underlying query
// rather than surfaced.
func (r *NotificationReadRepository) Exists(ctx context.Context, notificationID, userID int64) (bool, error) {
	return r.q.ExistsNotificationRead(ctx, sqlc.ExistsNotificationReadParams{
		NotificationID: notificationID,
		UserID:         userID,
	})
}

// ListByNotification returns a page of read rows for a single notification,
// using the supplied offset and limit for pagination.
//
// The row type is sqlc.ListNotificationReadsRow, which carries joined user
// context (name, email) so that the admin portal can render the reader list
// without a second query. offset and limit are passed through to SQL without
// validation, matching the convention used by the other list methods in this
// package.
func (r *NotificationReadRepository) ListByNotification(ctx context.Context, notificationID int64, offset, limit int32) ([]sqlc.ListNotificationReadsRow, error) {
	return r.q.ListNotificationReads(ctx, sqlc.ListNotificationReadsParams{
		NotificationID: notificationID,
		Offset:         offset,
		Limit:          limit,
	})
}
