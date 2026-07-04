package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

type Staff struct {
	ID           int64
	RoleID       int64
	Name         string
	Email        string
	PasswordHash string
	IsActive     bool
}

type StaffRepository struct {
	q sqlc.Querier
}

func NewStaffRepository(q sqlc.Querier) *StaffRepository {
	return &StaffRepository{
		q: q,
	}
}

func (r *StaffRepository) FindByID(ctx context.Context, id int64) (sqlc.StaffUser, error) {
	return r.q.GetStaffUserByID(ctx, id)
}

func (r *StaffRepository) FindByEmail(ctx context.Context, email string) (sqlc.StaffUser, error) {
	return r.q.GetStaffUserByEmail(ctx, email)
}

func (r *StaffRepository) Create(ctx context.Context, params sqlc.CreateStaffUserParams) (sqlc.StaffUser, error) {
	return r.q.CreateStaffUser(ctx, params)
}

func (r *StaffRepository) UpdatePassword(ctx context.Context, params sqlc.UpdateStaffPasswordParams) error {
	return r.q.UpdateStaffPassword(ctx, params)
}

func (r *StaffRepository) UpdateStatus(ctx context.Context, params sqlc.UpdateStaffStatusParams) error {
	return r.q.UpdateStaffStatus(ctx, params)
}

func (r *StaffRepository) List(ctx context.Context) ([]sqlc.ListStaffUsersRow, error) {
	return r.q.ListStaffUsers(ctx)
}
