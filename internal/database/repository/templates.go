package repository

import (
	"context"

	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

// TemplateRepository provides read and write access to the templates table.
//
// A template is a reusable notification definition — title, body, and
// optional channel metadata — from which notifications can be composed
// without re-authoring content each time. Templates are referenced by
// notifications at creation time (as a starting point) or by name in
// operator workflows; the repository exposes the standard set of CRUD
// operations plus a status toggle for retiring a template without deleting
// it.
//
// # Return-type variety
//
// Unlike most repositories in this package, several read methods here return
// joined row types rather than the base sqlc.Template, because the queries
// project usage metadata (for example, how many notifications reference the
// template). The row types are not interchangeable with each other, and the
// Godoc for each method names the exact type.
type TemplateRepository struct {
	q sqlc.Querier
}

// NewTemplateRepository constructs a TemplateRepository bound to q.
//
// q must be non-nil. Typical callers are the templates feature service
// (CRUD) and the notifications feature service (template resolution at
// create time). A transaction-scoped querier or a test fake is equally
// valid.
func NewTemplateRepository(q sqlc.Querier) *TemplateRepository {
	return &TemplateRepository{q: q}
}

// Count returns the total number of template rows, unfiltered.
//
// It is intended for admin dashboard totals and pagination metadata, not for
// per-request filtering. The count includes templates in every status,
// active or otherwise, unless the underlying query adds a predicate.
func (r *TemplateRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountTemplates(ctx)
}

// FindByID retrieves a template by its primary key.
//
// The return type is sqlc.GetTemplateByIDRow rather than the base
// sqlc.Template because the query joins in usage metadata (for example, the
// count of notifications derived from this template). pgx.ErrNoRows is
// returned unchanged when no row matches.
func (r *TemplateRepository) FindByID(ctx context.Context, id int64) (sqlc.GetTemplateByIDRow, error) {
	return r.q.GetTemplateByID(ctx, id)
}

// FindByName retrieves a template by its unique name.
//
// As with FindByID, the return type carries joined context beyond the base
// table columns. Template names are expected to be unique in the schema; a
// caller that wants a friendlier "name already taken" response should use
// ExistsByName before attempting a create. pgx.ErrNoRows is returned
// unchanged when no row matches.
func (r *TemplateRepository) FindByName(ctx context.Context, name string) (sqlc.GetTemplateByNameRow, error) {
	return r.q.GetTemplateByName(ctx, name)
}

// List returns a page of templates ordered by the query's default sort.
//
// The return type is sqlc.ListTemplatesRow, which joins usage metadata so
// the admin list view can display it without a second query. offset and
// limit are passed through to SQL without validation, following the package
// convention.
func (r *TemplateRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListTemplatesRow, error) {
	return r.q.ListTemplates(ctx, sqlc.ListTemplatesParams{
		Offset: offset,
		Limit:  limit,
	})
}

// Search returns a page of templates whose name or body matches keyword.
//
// As with the other Search methods in this package, keyword is wrapped in
// pgtype.Text{Valid: true} unconditionally: an empty keyword is treated as a
// valid search term rather than as "no filter". Callers that want "keyword
// is optional" semantics should use List. The returned row type carries the
// same joined context as List.
func (r *TemplateRepository) Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchTemplatesRow, error) {
	return r.q.SearchTemplates(ctx, sqlc.SearchTemplatesParams{
		Keyword: pgtype.Text{String: keyword, Valid: true},
		Offset:  offset,
		Limit:   limit,
	})
}

// Create inserts a new template and returns the sqlc-generated
// CreateTemplateRow projection.
//
// The row type is narrower than the base sqlc.Template model and exists
// because the insert path does not need the usage-metadata joins that the
// read paths require. As with other Create methods in this package, the
// repository does not enforce uniqueness on the template name — the schema
// does, and a duplicate will surface as a pgx error.
func (r *TemplateRepository) Create(ctx context.Context, params sqlc.CreateTemplateParams) (sqlc.CreateTemplateRow, error) {
	return r.q.CreateTemplate(ctx, params)
}

// Update applies a modification to a template's content or metadata.
//
// Which columns can be changed is defined by sqlc.UpdateTemplate. Editing a
// template does not retroactively alter notifications that were composed
// from it — those carry their own resolved content at the time of creation.
// The repository performs no validation of the template body itself; that is
// the caller's responsibility.
func (r *TemplateRepository) Update(ctx context.Context, params sqlc.UpdateTemplateParams) error {
	return r.q.UpdateTemplate(ctx, params)
}

// UpdateStatus changes the active/archived status of a template.
//
// Deactivating a template is the recommended way to retire it without
// breaking any audit history or foreign-key references. The method performs
// an unconditional UPDATE — it does not read the current status first — so
// applying a transition to a row already in the target state is a no-op at
// the SQL level.
func (r *TemplateRepository) UpdateStatus(ctx context.Context, params sqlc.UpdateTemplateStatusParams) error {
	return r.q.UpdateTemplateStatus(ctx, params)
}

// Delete removes a template by its primary key.
//
// This is a hard delete and is not the recommended way to retire a template
// whose history matters — prefer UpdateStatus to archive. Use Delete only
// when the caller has determined the row should no longer exist. The
// operation is subject to whatever foreign-key behaviour the schema
// declares on notifications that reference templates; the repository does
// not cascade or null out references itself. Deleting a non-existent row is
// not an error.
func (r *TemplateRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteTemplate(ctx, id)
}

// ExistsByName reports whether a template with the given name already exists.
//
// It is the cheap pre-check for Create and for name-changing Update calls.
// The method performs a single indexed lookup on the name and returns
// false, nil when no row is found — pgx.ErrNoRows is swallowed by the query
// rather than surfaced.
func (r *TemplateRepository) ExistsByName(ctx context.Context, name string) (bool, error) {
	return r.q.ExistsTemplateByName(ctx, name)
}
