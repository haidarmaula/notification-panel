package staff

import (
	"context"
	"hello/internal/audit"
	"hello/internal/database/sqlc"
)

// AuditService is the audit-logging contract required by the staff feature.
//
// It is a single-method interface that mirrors the concrete
// *audit.AuditService type's Log method exactly. The interface exists so
// that the staff service can be tested without a live audit-log
// repository, and so that a future revision that changes the audit
// package's shape does not ripple into the staff service as long as the
// contract is preserved.
//
// # Failure semantics
//
// The concrete implementation returns a non-nil error when JSON encoding
// of the audit payload fails, or when the underlying repository write
// fails. Callers in this codebase follow the convention of logging an
// audit failure but not aborting the surrounding operation — the staff
// service is expected to do the same, though the exact behaviour is
// documented on the service method that invokes Log.
type AuditService interface {
	Log(ctx context.Context, params audit.LogParams) error
}

// RoleRepository is the role-persistence contract required by the staff
// feature.
//
// The staff feature needs the ability to resolve a role name to a role
// record — for staff creation and role changes — and to look up a role by
// ID when populating the response. The methods declared here mirror the
// concrete repository's method set; documentation for each method's
// semantics is provided on the concrete repository in
// internal/database/repository/roles.go, not restated here.
//
// As with every interface in this codebase, the declaration is a
// testability seam: a mock implementation can be substituted in unit tests
// without a live PostgreSQL instance. The trade-off is that the interface
// must be kept in sync with the concrete repository's method set — adding
// a method to the concrete type does not automatically make it available
// here, and the signature of each method must match exactly, or the
// concrete repository will fail to satisfy this interface at the point
// where the module wires them together.
type RoleRepository interface {
	Count(ctx context.Context) (int64, error)
	FindByID(ctx context.Context, id int64) (sqlc.Role, error)
	FindByName(ctx context.Context, name string) (sqlc.Role, error)
	List(ctx context.Context, offset, limit int32) ([]sqlc.Role, error)
	Create(ctx context.Context, params sqlc.CreateRoleParams) (sqlc.Role, error)
	Update(ctx context.Context, params sqlc.UpdateRoleParams) error
	Delete(ctx context.Context, id int64) error
	ExistsByName(ctx context.Context, name string) (bool, error)
}

// StaffUserRepository is the staff-user persistence contract required by
// the staff feature.
//
// It mirrors the concrete *repository.StaffUserRepository's method set
// one-for-one. The staff service uses FindByID, FindByEmail, List,
// Search, Create, Update, UpdatePassword, UpdateStatus, Delete, and
// ExistsByEmail; the Count and other methods are declared for completeness
// and for consistency with the concrete repository's shape.
//
// The interface is the service's only means of accessing the staff_users
// table. Because the handler is also interface-typed (see StaffServicer
// in handler.go), the staff feature has the largest testable surface of
// any feature in the codebase — both the handler and the service can be
// tested with fakes rather than a live database.
//
// Documentation for each method's semantics — pagination conventions,
// error pass-through, NULL handling — is provided on the concrete
// repository in internal/database/repository/staff_users.go.
type StaffUserRepository interface {
	CountMatching(ctx context.Context, search string) (int64, error)
	FindByID(ctx context.Context, id int64) (sqlc.StaffUser, error)
	FindByEmail(ctx context.Context, email string) (sqlc.StaffUser, error)
	List(ctx context.Context, offset, limit int32) ([]sqlc.ListStaffUsersRow, error)
	Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchStaffUsersRow, error)
	Create(ctx context.Context, params sqlc.CreateStaffUserParams) (sqlc.StaffUser, error)
	Update(ctx context.Context, params sqlc.UpdateStaffUserParams) error
	UpdatePassword(ctx context.Context, params sqlc.UpdateStaffPasswordParams) error
	UpdateStatus(ctx context.Context, params sqlc.UpdateStaffStatusParams) error
	Delete(ctx context.Context, id int64) error
	ExistsByEmail(ctx context.Context, email string) (bool, error)
}
