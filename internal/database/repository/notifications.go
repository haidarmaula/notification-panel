package repository

import (
	"context"

	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

// NotificationRepository provides read and write access to the notifications
// table.
//
// A notification row is the campaign record itself: title, body, channel,
// schedule, and lifecycle status. It does not carry its audience — that
// lives in notification_targets — nor its per-device outcomes, which live in
// notification_deliveries. This repository is the primary abstraction the
// notifications feature service and the scheduler both depend on, so it is
// the largest repository in the package.
//
// # Lifecycle
//
// The status column follows the state machine documented in docs/database.md:
// DRAFT -> SCHEDULED -> QUEUED -> PROCESSING -> COMPLETED (with SENT and
// FAILED as terminal variants). Not every transition is exposed here as a
// dedicated method; UpdateStatus is the general-purpose escape hatch, while
// UpdateStatusIfScheduled and MarkSent exist because they encode atomic
// guarantees that a plain update cannot provide.
type NotificationRepository struct {
	q sqlc.Querier
}

// NewNotificationRepository constructs a NotificationRepository bound to q.
//
// q must be non-nil. The typical caller passes sqlc.New(db); in tests, a
// mock sqlc.Querier can be substituted without changing this type.
func NewNotificationRepository(q sqlc.Querier) *NotificationRepository {
	return &NotificationRepository{q: q}
}

// Count returns the total number of notification rows, unfiltered.
//
// It is intended for dashboard totals and pagination metadata, not for
// per-request filtering. Use CountWithFilters when the caller is displaying
// a filtered list.
func (r *NotificationRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountNotifications(ctx)
}

// CountWithFilters returns the number of notifications matching the supplied
// filters.
//
// The filter parameters are passed through to SQL verbatim. The expected
// convention — consistent with the admin portal's list endpoint — is that an
// empty string means "no filter on this column": status, targetType, and
// keyword are each optional. The repository does not normalise the inputs,
// so a caller that passes a whitespace-only keyword should expect the
// underlying query to treat it as a literal search term rather than as
// absent.
func (r *NotificationRepository) CountWithFilters(ctx context.Context, status, targetType, keyword string) (int64, error) {
	return r.q.CountNotificationsWithFilters(ctx, sqlc.CountNotificationsWithFiltersParams{
		Status:     status,
		TargetType: targetType,
		Keyword:    keyword,
	})
}

// Create inserts a new notification and returns the sqlc-generated
// CreateNotificationRow projection.
//
// The row type carries the inserted columns (plus any defaults the database
// fills in) but not the joined context exposed by FindByID. New
// notifications are expected to be inserted in DRAFT status; the database
// default and the caller's params must agree on this, as the repository does
// not enforce it.
func (r *NotificationRepository) Create(ctx context.Context, params sqlc.CreateNotificationParams) (sqlc.CreateNotificationRow, error) {
	return r.q.CreateNotification(ctx, params)
}

// FindByID retrieves a single notification by its primary key, joined with
// related context such as creator identity and aggregate delivery counts.
//
// The joined shape means the return type is not interchangeable with
// sqlc.CreateNotificationRow. When no row matches id, pgx.ErrNoRows is
// returned unchanged; callers translate it to a 404 at the HTTP layer.
func (r *NotificationRepository) FindByID(ctx context.Context, id int64) (sqlc.GetNotificationByIDRow, error) {
	return r.q.GetNotificationByID(ctx, id)
}

// List returns a page of notifications ordered by the query's default sort
// (typically newest first), without applying any filters.
//
// offset and limit are passed through to SQL without validation. This is the
// unfiltered listing variant; prefer ListWithFilters when the admin portal's
// filter bar is in play, so the pagination metadata matches the visible set.
func (r *NotificationRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListNotificationsRow, error) {
	return r.q.ListNotifications(ctx, sqlc.ListNotificationsParams{
		Offset: offset,
		Limit:  limit,
	})
}

// ListWithFilters returns a page of notifications matching the supplied
// filters.
//
// As with CountWithFilters, the expected convention is that empty strings
// mean "no filter on this column". Pair this call with CountWithFilters
// using the same arguments so that pagination totals match the returned
// page. offset and limit are passed through to SQL without validation.
func (r *NotificationRepository) ListWithFilters(ctx context.Context, status, targetType, keyword string, offset, limit int32) ([]sqlc.ListNotificationsWithFiltersRow, error) {
	return r.q.ListNotificationsWithFilters(ctx, sqlc.ListNotificationsWithFiltersParams{
		Status:     status,
		TargetType: targetType,
		Keyword:    keyword,
		Offset:     offset,
		Limit:      limit,
	})
}

// ListScheduledNotificationsDue returns scheduled notifications that are
// ready to be sent.
//
// This is the scheduler's hot path: it is called on every tick of the
// polling loop, with limit controlling how many rows a single tick will
// claim. The query is expected to select rows whose status is SCHEDULED and
// whose scheduled_at is in the past, ordered by scheduled_at so that older
// notifications are promoted first. The exact predicate and ordering are
// defined in db/queries/notifications.sql — the repository guarantees only
// that it returns at most limit rows.
//
// The returned row type is a narrow projection (typically id and
// scheduled_at) suitable for the scheduler's needs, not the full notification
// model.
func (r *NotificationRepository) ListScheduledNotificationsDue(ctx context.Context, limit int32) ([]sqlc.ListScheduledNotificationsDueRow, error) {
	return r.q.ListScheduledNotificationsDue(ctx, limit)
}

// Search returns a page of notifications whose text fields match keyword.
//
// Unlike ListWithFilters, keyword is wrapped in pgtype.Text{Valid: true}
// unconditionally before being passed to SQL. This means an empty keyword is
// treated as a valid (and therefore present) search term rather than as
// "no filter" — the resulting behavior depends on the SQL, but callers
// should not assume that Search(ctx, "", ...) is equivalent to List.
// Prefer ListWithFilters when the keyword is optional and may be empty.
//
// offset and limit are passed through to SQL without validation.
func (r *NotificationRepository) Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchNotificationsRow, error) {
	return r.q.SearchNotifications(ctx, sqlc.SearchNotificationsParams{
		Keyword: pgtype.Text{String: keyword, Valid: true},
		Offset:  offset,
		Limit:   limit,
	})
}

// Update applies a metadata update to a notification.
//
// The affected columns are defined by sqlc.UpdateNotification and the
// caller-supplied params. The repository performs no status guards or
// lifecycle checks: a caller that intends to only modify DRAFT notifications
// must enforce that at the service layer or by choosing a more specific
// method. For status transitions, prefer UpdateStatus, UpdateStatusIfScheduled,
// or MarkSent.
func (r *NotificationRepository) Update(ctx context.Context, params sqlc.UpdateNotificationParams) error {
	return r.q.UpdateNotification(ctx, params)
}

// UpdateStatus sets the status column on a notification to the value in
// params.
//
// This is the general-purpose transition method. It performs an
// unconditional UPDATE — the query does not read the current status first,
// so applying a transition that the lifecycle does not permit is not
// rejected here. Use UpdateStatusIfScheduled when the caller needs an atomic
// guard against a specific source state, which is the case for any
// concurrent-promotion scenario (notably the scheduler).
func (r *NotificationRepository) UpdateStatus(ctx context.Context, params sqlc.UpdateNotificationStatusParams) error {
	return r.q.UpdateNotificationStatus(ctx, params)
}

// UpdateStatusIfScheduled atomically transitions a notification's status only
// if the row is currently in SCHEDULED state.
//
// It returns the number of rows affected: 1 if the transition occurred, 0 if
// the row was already in a different state (or did not exist). This
// zero-or-one result is the sole reason the method exists as a distinct
// operation: the check-then-update is performed inside a single SQL statement
// so that concurrent callers — most importantly multiple scheduler replicas
// and any in-flight admin action — cannot race. A caller that reads the row
// first and then calls UpdateStatus would reopen exactly the race this
// method closes.
//
// The scheduler relies on this primitive to guarantee at-most-once promotion
// of a due notification to QUEUED: the winning replica sees rowsAffected == 1
// and proceeds to publish to Kafka; every other replica sees 0 and skips.
//
// Errors from the underlying query are returned unchanged. A driver-level
// failure is distinct from "no row matched" and must be handled by the
// caller as a real error rather than as a "lost the race" signal.
func (r *NotificationRepository) UpdateStatusIfScheduled(ctx context.Context, id int64, status string) (int64, error) {
	return r.q.UpdateNotificationStatusIfScheduled(ctx, sqlc.UpdateNotificationStatusIfScheduledParams{
		ID:     id,
		Status: status,
	})
}

// MarkSent records that a notification has finished dispatch.
//
// It is a named transition rather than a general status update so that the
// intent ("this notification has been sent") is visible at call sites and so
// that the SQL can set any secondary fields — such as sent_at — that a
// terminal transition requires but a routine status change does not. The
// repository does not enforce that the notification was previously in
// PROCESSING; that is a property of the SQL query.
func (r *NotificationRepository) MarkSent(ctx context.Context, id int64) error {
	return r.q.MarkNotificationSent(ctx, id)
}

// Delete removes a notification row by its primary key.
//
// This is a hard delete and does not cascade to notification_targets or
// notification_deliveries unless the schema declares ON DELETE CASCADE — the
// repository does not perform the cleanup itself. Callers that need to
// remove a notification and its dependents atomically should compose this
// with DeleteByNotification on the target repository inside a shared
// transaction. Deleting a non-existent row is not an error.
func (r *NotificationRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteNotification(ctx, id)
}

// GetStatistics returns aggregate delivery counts for a single notification.
//
// The row type carries multiple counters — typically the totals of
// deliveries in each terminal state (delivered, failed, opened) and the read
// count — assembled in a single query so the admin analytics view does not
// have to issue five round-trips. Because it returns a single Row rather
// than a slice, the underlying query is expected to always return exactly
// one row (using aggregate functions with COALESCE / COUNT(*) that yield zero
// rather than no rows), and to have no ErrNoRows case even for a
// notification with no deliveries.
func (r *NotificationRepository) GetStatistics(ctx context.Context, id int64) (sqlc.GetNotificationStatisticsRow, error) {
	return r.q.GetNotificationStatistics(ctx, id)
}
