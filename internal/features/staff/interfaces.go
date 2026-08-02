package staff

import (
	"context"
	"hello/internal/audit"
	"hello/internal/database/sqlc"
)

type AuditService interface {
	Log(ctx context.Context, params audit.LogParams) error
}

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

type StaffUserRepository interface {
	Count(ctx context.Context) (int64, error)
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
