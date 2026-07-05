package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

type UserRepository struct {
	q sqlc.Querier
}

func NewUserRepository(q sqlc.Querier) *UserRepository {
	return &UserRepository{q: q}
}

func (r *UserRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountUsers(ctx)
}

func (r *UserRepository) CountByStatus(ctx context.Context, status string) (int64, error) {
	return r.q.CountUsersByStatus(ctx, status)
}

func (r *UserRepository) FindByID(ctx context.Context, id int64) (sqlc.User, error) {
	return r.q.GetUserByID(ctx, id)
}

func (r *UserRepository) FindByExternalID(ctx context.Context, externalID string) (sqlc.User, error) {
	return r.q.GetUserByExternalID(ctx, externalID)
}

func (r *UserRepository) FindByEmail(ctx context.Context, email pgtype.Text) (sqlc.User, error) {
	return r.q.GetUserByEmail(ctx, email)
}

func (r *UserRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.User, error) {
	return r.q.ListUsers(ctx, sqlc.ListUsersParams{
		Offset: offset,
		Limit:  limit,
	})
}

func (r *UserRepository) ListByStatus(ctx context.Context, status string, offset, limit int32) ([]sqlc.User, error) {
	return r.q.ListUsersByStatus(ctx, sqlc.ListUsersByStatusParams{
		Status: status,
		Offset: offset,
		Limit:  limit,
	})
}

func (r *UserRepository) Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.User, error) {
	return r.q.SearchUsers(ctx, sqlc.SearchUsersParams{
		Keyword: pgtype.Text{String: keyword, Valid: true},
		Offset:  offset,
		Limit:   limit,
	})
}

func (r *UserRepository) Create(ctx context.Context, params sqlc.CreateUserParams) (sqlc.User, error) {
	return r.q.CreateUser(ctx, params)
}

func (r *UserRepository) Update(ctx context.Context, params sqlc.UpdateUserParams) error {
	return r.q.UpdateUser(ctx, params)
}

func (r *UserRepository) UpdateStatus(ctx context.Context, params sqlc.UpdateUserStatusParams) error {
	return r.q.UpdateUserStatus(ctx, params)
}

func (r *UserRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteUser(ctx, id)
}

func (r *UserRepository) ExistsByExternalID(ctx context.Context, externalID string) (bool, error) {
	return r.q.ExistsUserByExternalID(ctx, externalID)
}

func (r *UserRepository) ExistsByEmail(ctx context.Context, email pgtype.Text) (bool, error) {
	return r.q.ExistsUserByEmail(ctx, email)
}
