package repository

import (
	"context"

	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

type TemplateRepository struct {
	q sqlc.Querier
}

func NewTemplateRepository(q sqlc.Querier) *TemplateRepository {
	return &TemplateRepository{q: q}
}

func (r *TemplateRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountTemplates(ctx)
}

func (r *TemplateRepository) FindByID(ctx context.Context, id int64) (sqlc.GetTemplateByIDRow, error) {
	return r.q.GetTemplateByID(ctx, id)
}

func (r *TemplateRepository) FindByName(ctx context.Context, name string) (sqlc.GetTemplateByNameRow, error) {
	return r.q.GetTemplateByName(ctx, name)
}

func (r *TemplateRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListTemplatesRow, error) {
	return r.q.ListTemplates(ctx, sqlc.ListTemplatesParams{
		Offset: offset,
		Limit:  limit,
	})
}

func (r *TemplateRepository) Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchTemplatesRow, error) {
	return r.q.SearchTemplates(ctx, sqlc.SearchTemplatesParams{
		Keyword: pgtype.Text{String: keyword, Valid: true},
		Offset:  offset,
		Limit:   limit,
	})
}

func (r *TemplateRepository) Create(ctx context.Context, params sqlc.CreateTemplateParams) (sqlc.CreateTemplateRow, error) {
	return r.q.CreateTemplate(ctx, params)
}

func (r *TemplateRepository) Update(ctx context.Context, params sqlc.UpdateTemplateParams) error {
	return r.q.UpdateTemplate(ctx, params)
}

func (r *TemplateRepository) UpdateStatus(ctx context.Context, params sqlc.UpdateTemplateStatusParams) error {
	return r.q.UpdateTemplateStatus(ctx, params)
}

func (r *TemplateRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteTemplate(ctx, id)
}

func (r *TemplateRepository) ExistsByName(ctx context.Context, name string) (bool, error) {
	return r.q.ExistsTemplateByName(ctx, name)
}
