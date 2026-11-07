// Package repository is the data-access layer between the feature services
// and the sqlc-generated query code.
//
// # Purpose
//
// Every repository in this package wraps a single domain aggregate — audit
// logs, device tokens, notification deliveries, and so on — and exposes it as
// a set of intent-named methods (FindByID, MarkDelivered, ListByUser) rather
// than as raw sqlc calls. Feature services depend on these repositories,
// never on sqlc.Queries directly, so that the SQL layer can be regenerated,
// transaction-scoped, or mocked without touching business logic.
//
// # Design notes
//
//   - Constructors accept the sqlc.Querier interface rather than
//     *sqlc.Queries. This lets tests substitute a mock, and lets production
//     code bind a repository to a transaction-scoped querier
//     (sqlc.New(tx)) without changing its type.
//   - Repositories are stateless after construction: they hold only the
//     Querier reference and perform no caching or local buffering. They are
//     safe for concurrent use, provided the underlying Querier (typically a
//     pgxpool-backed *sqlc.Queries) is itself concurrency-safe, which it is.
//   - Pagination parameters (offset, limit) are passed through to SQL
//     verbatim. Bounds checking is the responsibility of the caller — in
//     practice the HTTP handler layer, which applies the API's documented
//     defaults (10) and caps (100).
//   - Methods that touch more than one table, or that need to be atomic with
//     a caller's other writes, should be composed by the caller using a
//     transaction. The repositories in this package do not open transactions
//     themselves.
package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

// AuditLogRepository provides read and write access to the audit_logs table.
//
// The repository is the persistence edge of the audit subsystem: it is
// consumed by internal/audit.AuditService, not directly by HTTP handlers.
// Audit rows are append-only from the application's point of view — there is
// no update or delete method — because the audit trail is intended to be
// immutable. Administrative cleanup, if ever needed, must be performed
// out-of-band.
type AuditLogRepository struct {
	q sqlc.Querier
}

// NewAuditLogRepository constructs an AuditLogRepository bound to q.
//
// q must be non-nil. The typical caller passes sqlc.New(db) where db is the
// pgxpool returned by database.New, but a transaction-scoped querier or a
// test fake is equally valid — the repository only relies on the Querier
// interface.
func NewAuditLogRepository(q sqlc.Querier) *AuditLogRepository {
	return &AuditLogRepository{q: q}
}

// Count returns the total number of rows in the audit_logs table.
//
// It is intended for administrative dashboards and pagination metadata, not
// for per-request work — the query is a full COUNT(*) and is not filtered.
// On large tables this is a linear scan unless the database has been
// configured with an appropriate index or a maintained counter.
func (r *AuditLogRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountAuditLogs(ctx)
}

// Create appends an audit row without requiring access to the audit history.
//
// The caller is expected to have already serialised any before/after
// snapshots to JSON — this method performs no encoding. It is a straight
// insert with no conflict handling: if the underlying table has unique
// constraints, a duplicate will surface as a pgx error from the driver and
// must be handled by the caller.
func (r *AuditLogRepository) Create(ctx context.Context, params sqlc.CreateAuditLogParams) error {
	return r.q.CreateAuditLog(ctx, params)
}

// FindByID retrieves a single audit log row by its primary key.
//
// The return type is sqlc.GetAuditLogByIDRow rather than sqlc.AuditLog
// because the sqlc query for this endpoint joins additional context (such as
// the acting staff user's name) beyond the raw table columns. If no row
// matches id, the underlying query returns pgx.ErrNoRows; the repository
// passes this through unchanged so callers can translate it to their own
// domain error (typically a 404 at the HTTP layer).
func (r *AuditLogRepository) FindByID(ctx context.Context, id int64) (sqlc.GetAuditLogByIDRow, error) {
	return r.q.GetAuditLogByID(ctx, id)
}

// List returns a page of audit log rows ordered by the query's default sort
// (typically newest first) using the supplied offset and limit.
//
// offset is the number of rows to skip; limit is the maximum number of rows
// to return. Neither value is validated or defaulted by the repository — a
// negative offset or a zero limit is passed through to PostgreSQL, which
// will either return an empty set or raise an error depending on the query.
// Callers in this codebase supply values that have already been normalised
// by the HTTP handler layer.
//
// The returned row type carries joined context (e.g. actor name) in addition
// to the base table columns, matching FindByID.
func (r *AuditLogRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListAuditLogsRow, error) {
	return r.q.ListAuditLogs(ctx, sqlc.ListAuditLogsParams{
		Offset: offset,
		Limit:  limit,
	})
}
