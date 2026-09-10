package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

// UploadBatchRepository provides read and write access to the upload_batches
// table.
//
// An upload batch is the parent record of a bulk-import operation — a single
// CSV or spreadsheet submission whose parsed lines are stored as individual
// upload_batch_rows. The repository is deliberately small: it exposes
// Create, FindByID, Count, and List, and no Update or UpdateStatus. That is
// an intentional divergence from the sibling UploadBatchRowRepository: a
// batch's lifecycle is expected to be advanced by a dedicated stage in the
// import pipeline rather than by arbitrary field edits from admin tooling,
// so no generic mutation method is provided here. If a future workflow
// requires per-batch status transitions, the convention in this package is
// to add a named transition method (as NotificationDeliveryRepository.Mark*
// does) rather than a general-purpose Update.
//
// A batch and its rows are owned together: the row repository is scoped by
// batch_id, and Delete is not exposed on either repository. Discarding a
// failed import is expected to be a schema-level cascade or an out-of-band
// cleanup operation, not a repository method.
type UploadBatchRepository struct {
	q sqlc.Querier
}

// NewUploadBatchRepository constructs an UploadBatchRepository bound to q.
//
// q must be non-nil. Typical callers are the upload/batch feature service
// and any worker that advances an import batch through its pipeline stages.
// A transaction-scoped querier is often the right choice when creating a
// batch and its first rows together.
func NewUploadBatchRepository(q sqlc.Querier) *UploadBatchRepository {
	return &UploadBatchRepository{q: q}
}

// Count returns the total number of upload batch rows, unfiltered.
//
// It is intended for admin dashboard totals and pagination metadata, not for
// per-request filtering. The count includes batches in every status; use the
// query's SQL definition to confirm whether any predicate is applied.
func (r *UploadBatchRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountUploadBatches(ctx)
}

// FindByID retrieves a single upload batch by its primary key and returns
// the base sqlc.UploadBatch model.
//
// The model carries the batch's own columns (filename, status, counts, and
// so on) but not its rows — those are queried separately via
// UploadBatchRowRepository.ListByBatch. pgx.ErrNoRows is returned unchanged
// when no row matches.
func (r *UploadBatchRepository) FindByID(ctx context.Context, id int64) (sqlc.UploadBatch, error) {
	return r.q.GetUploadBatchByID(ctx, id)
}

// List returns a page of upload batches ordered by the query's default sort
// (typically newest first).
//
// The return type is sqlc.ListUploadBatchesRow rather than the base
// sqlc.UploadBatch, which suggests the query joins in aggregate or
// contextual columns (such as the uploader's identity or a row count) for
// the admin list view. offset and limit are passed through to SQL without
// validation, following the package convention.
func (r *UploadBatchRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListUploadBatchesRow, error) {
	return r.q.ListUploadBatches(ctx, sqlc.ListUploadBatchesParams{
		Offset: offset,
		Limit:  limit,
	})
}

// Create inserts a new upload batch and returns the persisted row, including
// any database-generated fields.
//
// This method is expected to be called at the start of a bulk-import flow,
// before any rows are written: the caller creates the batch to obtain its
// ID, then passes that ID to UploadBatchRowRepository.Create for each parsed
// line. For atomicity between the batch record and its first rows, wrap both
// calls in a shared transaction using a transaction-scoped querier.
//
// The repository does not validate the batch's content or status; those are
// the caller's responsibility. If the schema enforces uniqueness on any
// column (such as a file hash to prevent duplicate uploads), a collision
// will surface as a pgx error.
func (r *UploadBatchRepository) Create(ctx context.Context, params sqlc.CreateUploadBatchParams) (sqlc.UploadBatch, error) {
	return r.q.CreateUploadBatch(ctx, params)
}
