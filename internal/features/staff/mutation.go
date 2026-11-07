package staff

import (
	"context"
	"errors"

	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/staffmutation"
)

// ErrLastActiveSuperAdmin prevents removing the final active account that can manage staff.
var ErrLastActiveSuperAdmin = errors.New("at least one active super administrator is required")

// StaffService binds the v1 administrative write, response and audit to one transaction.
type StaffService struct {
	*operations
	db staffmutation.Transactor
}

func bindStaffOperations(q *sqlc.Queries, permissions authz.Checker) *operations {
	staff := repository.NewStaffUserRepository(q)
	return newOperations(staff, repository.NewRoleRepository(q), audit.NewAuditService(repository.NewAuditLogRepository(q)), permissions)
}

// NewStaffService requires live authority checks and a transaction source for writes.
func NewStaffService(q *sqlc.Queries, db staffmutation.Transactor, permissions authz.Checker) *StaffService {
	return &StaffService{operations: bindStaffOperations(q, permissions), db: db}
}

func manage[T any](ctx context.Context, s *StaffService, target int64, removing bool, work func(*operations) (T, error)) (T, error) {
	if _, err := s.permissions.Require(ctx, authz.StaffManage); err != nil {
		var zero T
		return zero, err
	}
	return staffmutation.Run(ctx, s.db, target, true, func(q *sqlc.Queries) (T, error) {
		result, err := work(bindStaffOperations(q, authz.New(repository.NewStaffUserRepository(q))))
		if err != nil || !removing {
			return result, err
		}
		var zero T
		active, err := q.HasActiveSuperAdmin(ctx)
		if err != nil {
			return zero, err
		}
		if !active {
			return zero, ErrLastActiveSuperAdmin
		}
		return result, nil
	})
}

func (s *StaffService) Create(ctx context.Context, p CreateStaffParams) (*Staff, error) {
	return manage(ctx, s, 0, false, func(service *operations) (*Staff, error) { return service.Create(ctx, p) })
}
func (s *StaffService) Update(ctx context.Context, p UpdateStaffParams) (*Staff, error) {
	return manage(ctx, s, p.ID, p.Role != "" && p.Role != "SUPER_ADMIN", func(service *operations) (*Staff, error) { return service.Update(ctx, p) })
}
func (s *StaffService) UpdateStatus(ctx context.Context, id int64, active bool) (*Staff, error) {
	return manage(ctx, s, id, !active, func(service *operations) (*Staff, error) { return service.UpdateStatus(ctx, id, active) })
}
func (s *StaffService) Delete(ctx context.Context, id int64) error {
	_, err := manage(ctx, s, id, false, func(service *operations) (struct{}, error) {
		// Only an active super administrator can delete. The existing self-delete
		// rule guarantees the calling administrator survives a successful deletion.
		return struct{}{}, service.Delete(ctx, id)
	})
	return err
}
func (s *StaffService) UpdatePassword(ctx context.Context, id int64, current, password string) error {
	_, err := manage(ctx, s, id, false, func(service *operations) (struct{}, error) {
		return struct{}{}, service.UpdatePassword(ctx, id, current, password)
	})
	return err
}
