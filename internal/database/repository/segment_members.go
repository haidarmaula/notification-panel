package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

// SegmentMemberRepository provides read and write access to the
// segment_members table.
//
// A segment member is a (segment, user) association: user U belongs to
// segment S. The table is the join between segments and users and is the
// resolution point at which a segment-based notification target becomes a
// concrete set of user IDs the worker can fan out to. Because the same user
// can belong to many segments and the same segment contains many users, the
// repository exposes both directions of the relationship.
//
// Membership is expected to be maintained out-of-band — by an admin action
// or by a background job that evaluates segment criteria against the user
// table — rather than computed on the fly. This means a segment's membership
// is only as fresh as the last reconciliation, a property the notifications
// feature is aware of.
type SegmentMemberRepository struct {
	q sqlc.Querier
}

// NewSegmentMemberRepository constructs a SegmentMemberRepository bound to q.
//
// q must be non-nil. Typical callers are the segments feature service and
// the notification worker's audience-resolution stage.
func NewSegmentMemberRepository(q sqlc.Querier) *SegmentMemberRepository {
	return &SegmentMemberRepository{q: q}
}

// CountBySegment returns the number of members currently in a segment.
//
// This is the audience size as of the last reconciliation, not a live count
// derived from user attributes. The value is used for admin display and for
// sanity checks during target resolution; it is not a prediction of how
// many deliveries a notification will produce, since that also depends on
// how many active device tokens each member has.
func (r *SegmentMemberRepository) CountBySegment(ctx context.Context, segmentID int64) (int64, error) {
	return r.q.CountSegmentMembers(ctx, segmentID)
}

// CountSegmentsByUser returns the number of segments a user belongs to.
//
// It is a user-centric inverse of CountBySegment and is typically used on
// the user detail view to answer "how many audiences is this user part of?".
func (r *SegmentMemberRepository) CountSegmentsByUser(ctx context.Context, userID int64) (int64, error) {
	return r.q.CountSegmentMembersByUser(ctx, userID)
}

// Exists reports whether the membership pair (segmentID, userID) is already
// present in the segment_members table. It delegates to the generated
// ExistsSegmentMember query and is used by the members service to perform a
// correct duplicate check without scanning a paginated member list.
//
// A database error is returned unchanged. The method is safe for concurrent
// use because the underlying Querier is concurrency-safe.
func (r *SegmentMemberRepository) Exists(ctx context.Context, segmentID, userID int64) (bool, error) {
	return r.q.ExistsSegmentMember(ctx, sqlc.ExistsSegmentMemberParams{
		SegmentID: segmentID,
		UserID:    userID,
	})
}

// FindByID retrieves a single membership row by its primary key.
//
// Most callers do not need this: membership is naturally addressed by the
// (segment, user) pair, for which DeleteBySegmentAndUser is the intended
// removal operation. FindByID exists mainly for administrative tooling and
// for symmetry with the other repositories. pgx.ErrNoRows is returned
// unchanged when no row matches.
func (r *SegmentMemberRepository) FindByID(ctx context.Context, id int64) (sqlc.SegmentMember, error) {
	return r.q.GetSegmentMemberByID(ctx, id)
}

// ListBySegment returns a page of membership rows for a segment, joined with
// the member user's identity.
//
// The joined row type carries enough context (user name, email) for the
// admin portal to render the member list without a second query. offset and
// limit are passed through to SQL without validation, following the package
// convention. This is the paginated admin view of a segment; the worker's
// audience resolution uses a different, non-paginated query upstream of this
// repository.
func (r *SegmentMemberRepository) ListBySegment(ctx context.Context, segmentID int64, offset, limit int32) ([]sqlc.ListSegmentMembersRow, error) {
	return r.q.ListSegmentMembers(ctx, sqlc.ListSegmentMembersParams{
		SegmentID: segmentID,
		Offset:    offset,
		Limit:     limit,
	})
}

// ListSegmentsByUser returns all segments a user belongs to, paginated.
//
// It is the user-centric counterpart of ListBySegment and powers the "which
// audiences is this user in?" panel on the user detail page. offset and
// limit are passed through to SQL without validation.
func (r *SegmentMemberRepository) ListSegmentsByUser(ctx context.Context, userID int64, offset, limit int32) ([]sqlc.ListSegmentMembersByUserRow, error) {
	return r.q.ListSegmentMembersByUser(ctx, sqlc.ListSegmentMembersByUserParams{
		UserID: userID,
		Offset: offset,
		Limit:  limit,
	})
}

// Create inserts a new membership row, associating a user with a segment.
//
// The repository does not enforce that the pair is unique; the schema is
// expected to have a unique constraint on (segment_id, user_id), and a
// duplicate add will surface as a pgx error. Callers that need idempotent
// "add user to segment" semantics should either check first, use an upsert
// query, or tolerate the constraint violation.
func (r *SegmentMemberRepository) Create(ctx context.Context, params sqlc.CreateSegmentMemberParams) (sqlc.SegmentMember, error) {
	return r.q.CreateSegmentMember(ctx, params)
}

// Delete removes a membership row by its primary key.
//
// It is rarely the right method for the domain: membership is addressed by
// the (segment, user) pair, and DeleteBySegmentAndUser exists precisely so
// that callers do not need to look up the surrogate ID first. Delete is
// provided for administrative tooling and for symmetry with the rest of the
// package. Deleting a non-existent row is not an error.
func (r *SegmentMemberRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteSegmentMember(ctx, id)
}

// DeleteBySegmentAndUser removes the membership that associates a specific
// user with a specific segment.
//
// This is the natural "remove user from segment" operation and is the
// preferred form of deletion for the segments feature service, because it
// addresses the row by its domain identity rather than by a surrogate key.
// Removing a pair that does not exist is not an error; the underlying DELETE
// is a no-op in that case, so callers do not need to check existence first.
func (r *SegmentMemberRepository) DeleteBySegmentAndUser(ctx context.Context, segmentID, userID int64) error {
	return r.q.DeleteSegmentMemberBySegmentAndUser(ctx, sqlc.DeleteSegmentMemberBySegmentAndUserParams{
		SegmentID: segmentID,
		UserID:    userID,
	})
}
