package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

// UploadBatchRowRepository provides read and write access to the
// upload_batch_rows table.
//
// An upload batch row is a single parsed line from a bulk-import file —
// typically a CSV of user records staged for validation and eventual
// promotion into the users table. The repository is deliberately
// append-only: it exposes Create, CountByBatch, and ListByBatch, and
// nothing else. There is no Update and no Delete because staged rows are
// considered immutable once written; a batch that needs to be corrected is
// discarded and re-uploaded rather than edited in place.
//
// If a future workflow requires promoting or rejecting individual rows
// (setting a per-row status), the repository will grow a dedicated
// transition method rather than a general-purpose Update. This mirrors the
// pattern used by NotificationDeliveryRepository.Mark* methods.
type UploadBatchRowRepository struct {
	q sqlc.Querier
}

// NewUploadBatchRowRepository constructs an UploadBatchRowRepository bound
// to q.
//
// q must be non-nil. The typical caller passes sqlc.New(db); a
// transaction-scoped querier is often the right choice during batch import
// so that the parent batch and its rows are committed atomically.
func NewUploadBatchRowRepository(q sqlc.Querier) *UploadBatchRowRepository {
	return &UploadBatchRowRepository{q: q}
}

// CountByBatch returns the number of rows currently staged in a batch.
//
// It is used during and after import to report progress ("1,247 of 5,000
// rows parsed"), and by the parent batch's status computation to decide
// whether the import is complete. The count is a plain row count and does
// not distinguish between rows that will eventually pass or fail
// validation — that requires a per-status aggregate, which this repository
// does not currently expose.
func (r *UploadBatchRowRepository) CountByBatch(ctx context.Context, batchID int64) (int64, error) {
	return r.q.CountUploadBatchRows(ctx, batchID)
}

// ListByBatch returns a page of rows belonging to a single upload batch.
//
// offset and limit are passed through to SQL without validation, following
// the package convention. The return type is the base sqlc.UploadBatchRow
// rather than a joined projection, since a row's useful data is entirely
// local to the row itself; no cross-table context is needed for the review
// UI. Ordering is defined by the query, typically by the row's line number
// so that the UI can present the file in original order.
func (r *UploadBatchRowRepository) ListByBatch(ctx context.Context, batchID int64, offset, limit int32) ([]sqlc.UploadBatchRow, error) {
	return r.q.ListUploadBatchRows(ctx, sqlc.ListUploadBatchRowsParams{
		BatchID: batchID,
		Offset:  offset,
		Limit:   limit,
	})
}

// Create inserts a single upload batch row.
//
// It is called once per parsed line by the batch import pipeline. The
// repository performs no validation of the row's content — malformed lines
// are expected to be captured with an error/reason column populated by the
// caller, not rejected here — and does not enforce uniqueness within a
// batch. Bulk imports that need throughput beyond a per-row INSERT should
// use a batched copy mechanism at a higher layer rather than calling Create
// in a tight loop.
func (r *UploadBatchRowRepository) Create(ctx context.Context, params sqlc.CreateUploadBatchRowParams) (sqlc.UploadBatchRow, error) {
	return r.q.CreateUploadBatchRow(ctx, params)
}
