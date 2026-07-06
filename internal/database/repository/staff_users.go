package repository

import (
	"context"

	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

// StaffUserRepository provides database access for staff users.
type StaffUserRepository struct {
	q sqlc.Querier
}

// NewStaffUserRepository creates a new StaffUserRepository instance.
func NewStaffUserRepository(q sqlc.Querier) *StaffUserRepository {
	return &StaffUserRepository{q: q}
}

// Count returns the total number of staff users.
func (r *StaffUserRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountStaffUsers(ctx)
}

// FindByID retrieves a staff user by ID.
func (r *StaffUserRepository) FindByID(ctx context.Context, id int64) (sqlc.StaffUser, error) {
	return r.q.GetStaffUserByID(ctx, id)
}

// FindByEmail retrieves a staff user by email.
func (r *StaffUserRepository) FindByEmail(ctx context.Context, email string) (sqlc.StaffUser, error) {
	return r.q.GetStaffUserByEmail(ctx, email)
}

// List returns a paginated list of staff users with role names.
func (r *StaffUserRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListStaffUsersRow, error) {
	return r.q.ListStaffUsers(ctx, sqlc.ListStaffUsersParams{
		Offset: offset,
		Limit:  limit,
	})
}

// Search returns a paginated list of staff users matching a keyword (by name or email).
func (r *StaffUserRepository) Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchStaffUsersRow, error) {
	return r.q.SearchStaffUsers(ctx, sqlc.SearchStaffUsersParams{
		Keyword: pgtype.Text{String: keyword, Valid: true},
		Offset:  offset,
		Limit:   limit,
	})
}

// Create inserts a new staff user record.
func (r *StaffUserRepository) Create(ctx context.Context, params sqlc.CreateStaffUserParams) (sqlc.StaffUser, error) {
	return r.q.CreateStaffUser(ctx, params)
}

// Update modifies an existing staff user's role, name, or email.
func (r *StaffUserRepository) Update(ctx context.Context, params sqlc.UpdateStaffUserParams) error {
	return r.q.UpdateStaffUser(ctx, params)
}

// UpdatePassword changes the password hash of a staff user.
func (r *StaffUserRepository) UpdatePassword(ctx context.Context, params sqlc.UpdateStaffPasswordParams) error {
	return r.q.UpdateStaffPassword(ctx, params)
}

// UpdateStatus changes the active status of a staff user.
func (r *StaffUserRepository) UpdateStatus(ctx context.Context, params sqlc.UpdateStaffStatusParams) error {
	return r.q.UpdateStaffStatus(ctx, params)
}

// Delete removes a staff user by ID.
func (r *StaffUserRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteStaffUser(ctx, id)
}

// ExistsByEmail checks whether a staff user with the given email already exists.
func (r *StaffUserRepository) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	return r.q.ExistsStaffUserByEmail(ctx, email)
}
