package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

// UserRepository provides user creation and external ID lookup for the
// PostgreSQL integration fixture.
type UserRepository struct {
	q sqlc.Querier
}

// NewUserRepository constructs a UserRepository bound to q.
func NewUserRepository(q sqlc.Querier) *UserRepository {
	return &UserRepository{q: q}
}

// FindByExternalID returns pgx.ErrNoRows when no user matches externalID.
func (r *UserRepository) FindByExternalID(ctx context.Context, externalID string) (sqlc.User, error) {
	return r.q.GetUserByExternalID(ctx, externalID)
}

// Create inserts a user and returns the persisted row.
func (r *UserRepository) Create(ctx context.Context, params sqlc.CreateUserParams) (sqlc.User, error) {
	return r.q.CreateUser(ctx, params)
}
