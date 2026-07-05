package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

type RoleRepository struct {
	q sqlc.Querier
}

func NewRoleRepository(q sqlc.Querier) *RoleRepository {
	return &RoleRepository{q: q}
}

func (r *RoleRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountRoles(ctx)
}

func (r *RoleRepository) FindByID(ctx context.Context, id int64) (sqlc.Role, error) {
	return r.q.GetRoleByID(ctx, id)
}

func (r *RoleRepository) FindByName(ctx context.Context, name string) (sqlc.Role, error) {
	return r.q.GetRoleByName(ctx, name)
}

func (r *RoleRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.Role, error) {
	return r.q.ListRoles(ctx, sqlc.ListRolesParams{
		Offset: offset,
		Limit:  limit,
	})
}

func (r *RoleRepository) Create(ctx context.Context, params sqlc.CreateRoleParams) (sqlc.Role, error) {
	return r.q.CreateRole(ctx, params)
}

func (r *RoleRepository) Update(ctx context.Context, params sqlc.UpdateRoleParams) error {
	return r.q.UpdateRole(ctx, params)
}

func (r *RoleRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteRole(ctx, id)
}

func (r *RoleRepository) ExistsByName(ctx context.Context, name string) (bool, error) {
	return r.q.ExistsRoleByName(ctx, name)
}
