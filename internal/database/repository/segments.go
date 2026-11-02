package repository

import (
	"context"

	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

// SegmentRepository provides read and write access to the segments table.
//
// A segment is a named audience definition: a human-readable label, an
// optional filter or criteria expression, and a stable identity used by
// notification targets to reference it. The repository stores only the
// segment's metadata — membership is held separately in segment_members and
// is maintained out-of-band (see SegmentMemberRepository).
//
// Segments are referenced by notification_targets. Delete is therefore
// subject to a foreign-key constraint unless the schema declares ON DELETE
// CASCADE; the repository does not perform any coordinated cleanup itself.
type SegmentRepository struct {
	q sqlc.Querier
}

// NewSegmentRepository constructs a SegmentRepository bound to q.
//
// q must be non-nil. Typical callers are the segments feature service (which
// owns CRUD) and the notification worker's audience-resolution stage (which
// reads segment membership by segment ID).
func NewSegmentRepository(q sqlc.Querier) *SegmentRepository {
	return &SegmentRepository{q: q}
}

// Count returns the total number of segment rows, unfiltered.
//
// It is intended for admin dashboard totals and pagination metadata, not for
// per-request filtering. On large tables this is a linear COUNT(*) unless
// the database has been configured to optimise it.
func (r *SegmentRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountSegments(ctx)
}

// CountSearch returns the number of segments whose name matches keyword.
//
// It pairs with Search for filtered pagination totals. The keyword is
// wrapped in a valid pgtype.Text value; an empty keyword matches every
// segment because an empty search term matches every name, so
// callers should use Count when keyword is empty.
func (r *SegmentRepository) CountSearch(ctx context.Context, keyword string) (int64, error) {
	return r.q.CountSegmentsWithSearch(ctx, pgtype.Text{String: keyword, Valid: true})
}

// FindByID retrieves a segment by its primary key and returns the base
// sqlc.Segment model.
//
// pgx.ErrNoRows is returned unchanged when no row matches; callers at the
// HTTP layer translate it to a 404. This is the lookup used both by the
// segment detail endpoint and by the worker when resolving a
// segment-based notification target.
func (r *SegmentRepository) FindByID(ctx context.Context, id int64) (sqlc.Segment, error) {
	return r.q.GetSegmentByID(ctx, id)
}

// FindByName retrieves a segment by its unique name.
//
// The segment name is a stable, human-readable identifier and is expected to
// be unique across the table. As with FindByID, pgx.ErrNoRows is passed
// through unchanged. Callers that want a friendlier "name already taken"
// response should use ExistsByName before attempting a create.
func (r *SegmentRepository) FindByName(ctx context.Context, name string) (sqlc.Segment, error) {
	return r.q.GetSegmentByName(ctx, name)
}

// List returns a page of segments ordered by the query's default sort,
// without applying any filters.
//
// The returned row type is sqlc.ListSegmentsRow rather than sqlc.Segment
// because the query joins in aggregate context — typically the current
// member count — so the admin list view can display audience size without a
// second round trip. offset and limit are passed through to SQL without
// validation, following the package convention.
func (r *SegmentRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListSegmentsRow, error) {
	return r.q.ListSegments(ctx, sqlc.ListSegmentsParams{
		Offset: offset,
		Limit:  limit,
	})
}

// Search returns a page of segments whose name matches keyword.
//
// Unlike the filter-based list methods elsewhere in this package, keyword is
// wrapped in pgtype.Text{Valid: true} unconditionally: an empty keyword is
// treated as a valid search term rather than as "no filter", and the
// behaviour for that case is defined by the SQL. Callers that want
// "keyword is optional" semantics should use List instead.
//
// The returned row type carries the same joined context as List, so
// pagination metadata computed via Count will not exactly match a search
// result count — callers displaying a search should pair with a dedicated
// count query if such a query exists, or accept that the total is
// approximate.
func (r *SegmentRepository) Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchSegmentsRow, error) {
	return r.q.SearchSegments(ctx, sqlc.SearchSegmentsParams{
		Keyword: pgtype.Text{String: keyword, Valid: true},
		Offset:  offset,
		Limit:   limit,
	})
}

// Create inserts a new segment and returns the persisted row, including any
// database-generated fields.
//
// The repository does not validate that the name is unique; the schema is
// expected to enforce that constraint, and a duplicate name will surface as
// a pgx error. Callers that want to produce a friendlier "segment already
// exists" response should call ExistsByName first, or inspect the returned
// error for a unique-violation code.
func (r *SegmentRepository) Create(ctx context.Context, params sqlc.CreateSegmentParams) (sqlc.Segment, error) {
	return r.q.CreateSegment(ctx, params)
}

// Update applies a modification to a segment's metadata.
//
// Which columns can be changed is defined by sqlc.UpdateSegment. Updating a
// segment does not retroactively change the audience of any notification
// that has already been dispatched — those deliveries were resolved against
// the membership at send time and are stored in notification_deliveries.
// Future notifications that reference the segment will observe the new
// state.
func (r *SegmentRepository) Update(ctx context.Context, params sqlc.UpdateSegmentParams) error {
	return r.q.UpdateSegment(ctx, params)
}

// Delete removes a segment row by its primary key.
//
// The operation is subject to whatever foreign-key behaviour the schema
// declares on notification_targets and segment_members. A segment that is
// still referenced by an active notification target will typically fail with
// a foreign-key violation, which the repository passes through unchanged;
// the caller decides whether that is an error to surface or a signal to
// clean up targets first. Deleting a non-existent row is not an error.
func (r *SegmentRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteSegment(ctx, id)
}

// ExistsByName reports whether a segment with the given name already exists.
//
// It is the cheap pre-check for Create: a caller that wants to avoid
// triggering a unique-constraint violation can call this first. The method
// performs a single indexed lookup on the name and returns false, nil when
// no row is found — pgx.ErrNoRows is swallowed by the query rather than
// surfaced.
func (r *SegmentRepository) ExistsByName(ctx context.Context, name string) (bool, error) {
	return r.q.ExistsSegmentByName(ctx, name)
}
