package repository

import (
	"context"

	"hello/internal/database/sqlc"
)

// RoleRepository provides read and write access to the roles table.
//
// Roles are the coarse-grained authorization primitive: every staff user
// references exactly one role, and the middleware layer compares role names
// against configuration to make super-admin decisions. Because roles are
// referenced by staff users, Delete on this repository is subject to a
// foreign-key constraint unless the schema defines ON DELETE CASCADE or SET
// NULL — the repository does not perform any reassignment itself.
//
// The canonical role names (SUPER_ADMIN, ADMIN, OPERATOR) are established by
// the bootstrap utility. This repository treats the name as an opaque
// string; nothing here validates against a fixed set.
type RoleRepository struct {
	q sqlc.Querier
}

// NewRoleRepository constructs a RoleRepository bound to q.
//
// q must be non-nil. Typical callers are the bootstrap utility and the staff
// feature service; the API's read paths use the repository for role
// resolution during staff creation and login.
func NewRoleRepository(q sqlc.Querier) *RoleRepository {
	return &RoleRepository{q: q}
}

// Count returns the total number of role rows.
//
// It is intended for administrative dashboards and readiness checks, not for
// per-request work. Because the roles table is expected to hold a small,
// fixed set of rows, this query is inexpensive even when called frequently.
func (r *RoleRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountRoles(ctx)
}

// FindByID retrieves a role by its primary key.
//
// pgx.ErrNoRows is returned unchanged when no row matches, and callers at
// the HTTP layer translate it to a 404. This method is typically used when
// rendering a staff user's role for an admin detail view.
func (r *RoleRepository) FindByID(ctx context.Context, id int64) (sqlc.Role, error) {
	return r.q.GetRoleByID(ctx, id)
}

// FindByName retrieves a role by its unique name.
//
// It is used during bootstrap and login. As with FindByID, pgx.ErrNoRows is
// passed through unchanged; bootstrap uses it to detect that migrations have
// not been applied.
func (r *RoleRepository) FindByName(ctx context.Context, name string) (sqlc.Role, error) {
	return r.q.GetRoleByName(ctx, name)
}

// List returns a page of roles using the supplied offset and limit for
// pagination.
//
// offset and limit are passed through to SQL verbatim; the repository
// performs no bounds checking or defaulting. The ordering is defined by the
// underlying query, not by this method.
func (r *RoleRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.Role, error) {
	return r.q.ListRoles(ctx, sqlc.ListRolesParams{
		Offset: offset,
		Limit:  limit,
	})
}

// Create inserts a new role and returns the persisted row, including any
// database-generated fields.
//
// The repository does not validate that the name is unique — the schema is
// expected to enforce that constraint, and a collision will surface as a
// pgx error. Callers that want to produce a friendlier "role already exists"
// response should check ExistsByName first, or inspect the returned error
// for a unique-violation code.
func (r *RoleRepository) Create(ctx context.Context, params sqlc.CreateRoleParams) (sqlc.Role, error) {
	return r.q.CreateRole(ctx, params)
}

// Update applies a modification to a role.
//
// Which columns can be changed is defined by sqlc.UpdateRole. The repository
// performs no checks against role-name immutability or against updates that
// would orphan staff users; those are policy decisions that belong to the
// caller. Because staff users reference roles by ID, renaming a role does
// not require any coordinated update on the staff side.
func (r *RoleRepository) Update(ctx context.Context, params sqlc.UpdateRoleParams) error {
	return r.q.UpdateRole(ctx, params)
}

// Delete removes a role row by its primary key.
//
// The operation is subject to whatever foreign-key behavior the schema
// declares on staff_users.role_id. A role that is still referenced by any
// staff user will typically fail with a foreign-key violation, which the
// repository passes through unchanged; the caller decides whether that is an
// error to surface or a signal to reassign users first. Deleting a
// non-existent row is not an error.
func (r *RoleRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteRole(ctx, id)
}

// ExistsByName reports whether a role with the given name already exists.
//
// It is the cheap pre-check for Create: a caller that wants to avoid
// triggering a unique-constraint violation can call this first. The method
// performs a single indexed lookup on the name and returns false, nil when
// no row is found — pgx.ErrNoRows is swallowed by the query rather than
// surfaced.
func (r *RoleRepository) ExistsByName(ctx context.Context, name string) (bool, error) {
	return r.q.ExistsRoleByName(ctx, name)
}
