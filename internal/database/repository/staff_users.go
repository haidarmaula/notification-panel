package repository

import (
	"context"

	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

type StaffUserRepository struct {
	q sqlc.Querier
}

func NewStaffUserRepository(q sqlc.Querier) *StaffUserRepository {
	return &StaffUserRepository{q: q}
}

func (r *StaffUserRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountStaffUsers(ctx)
}

func (r *StaffUserRepository) FindByID(ctx context.Context, id int64) (sqlc.StaffUser, error) {
	return r.q.GetStaffUserByID(ctx, id)
}

func (r *StaffUserRepository) FindByEmail(ctx context.Context, email string) (sqlc.StaffUser, error) {
	return r.q.GetStaffUserByEmail(ctx, email)
}

func (r *StaffUserRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListStaffUsersRow, error) {
	return r.q.ListStaffUsers(ctx, sqlc.ListStaffUsersParams{
		Offset: offset,
		Limit:  limit,
	})
}

func (r *StaffUserRepository) Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchStaffUsersRow, error) {
	return r.q.SearchStaffUsers(ctx, sqlc.SearchStaffUsersParams{
		Keyword: pgtype.Text{String: keyword, Valid: true},
		Offset:  offset,
		Limit:   limit,
	})
}

func (r *StaffUserRepository) Create(ctx context.Context, params sqlc.CreateStaffUserParams) (sqlc.StaffUser, error) {
	return r.q.CreateStaffUser(ctx, params)
}

func (r *StaffUserRepository) Update(ctx context.Context, params sqlc.UpdateStaffUserParams) error {
	return r.q.UpdateStaffUser(ctx, params)
}

func (r *StaffUserRepository) UpdatePassword(ctx context.Context, params sqlc.UpdateStaffPasswordParams) error {
	return r.q.UpdateStaffPassword(ctx, params)
}

func (r *StaffUserRepository) UpdateStatus(ctx context.Context, params sqlc.UpdateStaffStatusParams) error {
	return r.q.UpdateStaffStatus(ctx, params)
}

func (r *StaffUserRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteStaffUser(ctx, id)
}

func (r *StaffUserRepository) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	return r.q.ExistsStaffUserByEmail(ctx, email)
}
