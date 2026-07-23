package staff

import (
	"context"
	"errors"
	"fmt"
	"log"

	"hello/internal/audit"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"

	"golang.org/x/crypto/bcrypt"
)

// Domain errors for staff service.
var (
	ErrEmailAlreadyRegistered = errors.New("email already registered")
	ErrInvalidRole            = errors.New("invalid role")
	ErrStaffNotFound          = errors.New("staff user not found")
	ErrEmailAlreadyUsed       = errors.New("email already used by another staff")
)

// CreateStaffParams holds the input for creating a new staff user.
type CreateStaffParams struct {
	Role     string
	Name     string
	Email    string
	Password string
}

// UpdateStaffParams holds the input for updating a staff user.
type UpdateStaffParams struct {
	ID    int64
	Role  string
	Name  string
	Email string
}

// StaffService provides business logic for staff management.
type StaffService struct {
	staffRepo    *repository.StaffUserRepository
	roleRepo     *repository.RoleRepository
	auditService *audit.AuditService
}

// NewStaffService creates a new StaffService instance.
func NewStaffService(staffRepo *repository.StaffUserRepository, roleRepo *repository.RoleRepository, auditService *audit.AuditService) *StaffService {
	return &StaffService{
		staffRepo:    staffRepo,
		roleRepo:     roleRepo,
		auditService: auditService,
	}
}

// Create creates a new staff user.
// It validates email uniqueness, hashes the password, and assigns a role.
// Returns the created Staff object or an error.
// Possible errors: ErrEmailAlreadyRegistered, ErrInvalidRole.
func (s *StaffService) Create(ctx context.Context, params CreateStaffParams) (*Staff, error) {
	exists, err := s.staffRepo.ExistsByEmail(ctx, params.Email)
	if err != nil {
		return nil, fmt.Errorf("check email: %w", err)
	}
	if exists {
		return nil, ErrEmailAlreadyRegistered
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	role, err := s.roleRepo.FindByName(ctx, params.Role)
	if err != nil {
		return nil, ErrInvalidRole
	}

	staff, err := s.staffRepo.Create(ctx, sqlc.CreateStaffUserParams{
		RoleID:       role.ID,
		Name:         params.Name,
		Email:        params.Email,
		PasswordHash: string(hashed),
	})
	if err != nil {
		return nil, fmt.Errorf("create staff: %w", err)
	}

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_STAFF_CREATE,
		EntityType: audit.ENTITY_TYPE_STAFF,
		EntityName: staff.Name,
		EntityID:   staff.ID,
		After:      staff,
	}); errLog != nil {
		log.Printf(
			"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
			audit.ACTION_STAFF_CREATE,
			audit.ENTITY_TYPE_STAFF,
			staff.ID,
			staff.Name,
			errLog,
		)
	}

	return &Staff{
		ID:        staff.ID,
		RoleID:    staff.RoleID,
		RoleName:  role.Name,
		Name:      staff.Name,
		Email:     staff.Email,
		IsActive:  staff.IsActive,
		CreatedAt: staff.CreatedAt.Time,
		UpdatedAt: staff.UpdatedAt.Time,
	}, nil
}

// GetByID retrieves a staff user by ID along with their role name.
// Returns ErrStaffNotFound if the user does not exist.
func (s *StaffService) GetByID(ctx context.Context, id int64) (*Staff, error) {
	staff, err := s.staffRepo.FindByID(ctx, id)
	if err != nil {
		return nil, ErrStaffNotFound
	}

	role, err := s.roleRepo.FindByID(ctx, staff.RoleID)
	if err != nil {
		return nil, fmt.Errorf("get role: %w", err)
	}

	return &Staff{
		ID:        staff.ID,
		RoleID:    staff.RoleID,
		RoleName:  role.Name,
		Name:      staff.Name,
		Email:     staff.Email,
		IsActive:  staff.IsActive,
		CreatedAt: staff.CreatedAt.Time,
		UpdatedAt: staff.UpdatedAt.Time,
	}, nil
}

// List returns a paginated list of staff users with optional search by name or email.
// Returns the list of Staff objects, total count, and any error.
func (s *StaffService) List(ctx context.Context, page, limit int32, search string) ([]Staff, int64, error) {
	offset := (page - 1) * limit

	var staffs []Staff
	var total int64
	var err error

	if search != "" {
		rows, err := s.staffRepo.Search(ctx, search, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("search staff: %w", err)
		}

		staffs = make([]Staff, len(rows))
		for i, row := range rows {
			staffs[i] = Staff{
				ID:        row.ID,
				RoleID:    row.RoleID,
				RoleName:  row.RoleName,
				Name:      row.Name,
				Email:     row.Email,
				IsActive:  row.IsActive,
				CreatedAt: row.CreatedAt.Time,
				UpdatedAt: row.UpdatedAt.Time,
			}
		}
	} else {
		rows, err := s.staffRepo.List(ctx, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("list staff: %w", err)
		}

		staffs = make([]Staff, len(rows))
		for i, row := range rows {
			staffs[i] = Staff{
				ID:        row.ID,
				RoleID:    row.RoleID,
				RoleName:  row.RoleName,
				Name:      row.Name,
				Email:     row.Email,
				IsActive:  row.IsActive,
				CreatedAt: row.CreatedAt.Time,
				UpdatedAt: row.UpdatedAt.Time,
			}
		}
	}

	total, err = s.staffRepo.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count staff: %w", err)
	}

	return staffs, total, nil
}

// Update updates a staff user's role, name, or email.
// Returns the updated Staff object or an error.
// Possible errors: ErrStaffNotFound, ErrInvalidRole, ErrEmailAlreadyUsed.
func (s *StaffService) Update(ctx context.Context, params UpdateStaffParams) (*Staff, error) {
	existing, err := s.staffRepo.FindByID(ctx, params.ID)
	if err != nil {
		return nil, ErrStaffNotFound
	}

	update := sqlc.UpdateStaffUserParams{
		ID:     params.ID,
		RoleID: existing.RoleID,
		Name:   existing.Name,
		Email:  existing.Email,
	}

	if params.Role != "" {
		role, err := s.roleRepo.FindByName(ctx, params.Role)
		if err != nil {
			return nil, ErrInvalidRole
		}
		update.RoleID = role.ID
	}

	if params.Name != "" {
		update.Name = params.Name
	}

	if params.Email != "" && params.Email != existing.Email {
		exists, err := s.staffRepo.ExistsByEmail(ctx, params.Email)
		if err != nil {
			return nil, fmt.Errorf("check email: %w", err)
		}
		if exists {
			return nil, ErrEmailAlreadyUsed
		}
		update.Email = params.Email
	}

	if err := s.staffRepo.Update(ctx, update); err != nil {
		return nil, fmt.Errorf("update staff: %w", err)
	}

	staff, err := s.GetByID(ctx, params.ID)
	if err == nil {
		if errLog := s.auditService.Log(ctx, audit.LogParams{
			Action:     audit.ACTION_STAFF_UPDATE,
			EntityType: audit.ENTITY_TYPE_STAFF,
			EntityName: staff.Name,
			EntityID:   staff.ID,
			Before:     existing,
			After:      staff,
		}); errLog != nil {
			log.Printf(
				"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
				audit.ACTION_STAFF_UPDATE,
				audit.ENTITY_TYPE_STAFF,
				staff.ID,
				staff.Name,
				errLog,
			)
		}
	}

	return staff, err
}

// UpdateStatus changes the active status of a staff user.
// Returns the updated Staff object or an error.
// Possible error: ErrStaffNotFound.
func (s *StaffService) UpdateStatus(ctx context.Context, id int64, isActive bool) (*Staff, error) {
	existing, err := s.staffRepo.FindByID(ctx, id)
	if err != nil {
		return nil, ErrStaffNotFound
	}

	if err := s.staffRepo.UpdateStatus(ctx, sqlc.UpdateStaffStatusParams{ID: id, IsActive: isActive}); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}

	staff, err := s.GetByID(ctx, id)
	if err == nil {
		if errLog := s.auditService.Log(ctx, audit.LogParams{
			Action:     audit.ACTION_STAFF_UPDATE_STATUS,
			EntityType: audit.ENTITY_TYPE_STAFF,
			EntityName: staff.Name,
			EntityID:   staff.ID,
			Before:     existing,
			After:      staff,
		}); errLog != nil {
			log.Printf(
				"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
				audit.ACTION_STAFF_UPDATE_STATUS,
				audit.ENTITY_TYPE_STAFF,
				staff.ID,
				staff.Name,
				errLog,
			)
		}
	}

	return staff, err
}

// UpdatePassword hashes and updates the password of a staff user.
// Returns an error if the user is not found or hashing fails.
// Possible error: ErrStaffNotFound.
func (s *StaffService) UpdatePassword(ctx context.Context, id int64, newPassword string) error {
	existing, err := s.staffRepo.FindByID(ctx, id)
	if err != nil {
		return ErrStaffNotFound
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	err = s.staffRepo.UpdatePassword(ctx, sqlc.UpdateStaffPasswordParams{
		ID:           id,
		PasswordHash: string(hashed),
	})

	if err == nil {
		if errLog := s.auditService.Log(ctx, audit.LogParams{
			Action:     audit.ACTION_STAFF_UPDATE_PASSWORD,
			EntityType: audit.ENTITY_TYPE_STAFF,
			EntityName: existing.Name,
			EntityID:   existing.ID,
		}); errLog != nil {
			log.Printf(
				"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
				audit.ACTION_STAFF_UPDATE_PASSWORD,
				audit.ENTITY_TYPE_STAFF,
				existing.ID,
				existing.Name,
				errLog,
			)
		}
	}

	return err
}

func (s *StaffService) Delete(ctx context.Context, id int64) error {
	existing, err := s.staffRepo.FindByID(ctx, id)
	if err != nil {
		return ErrStaffNotFound
	}

	err = s.staffRepo.Delete(ctx, id)
	if err == nil {
		if errLog := s.auditService.Log(ctx, audit.LogParams{
			Action:     audit.ACTION_STAFF_DELETE,
			EntityType: audit.ENTITY_TYPE_STAFF,
			EntityName: existing.Name,
			EntityID:   existing.ID,
			Before:     existing,
		}); errLog != nil {
			log.Printf(
				"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
				audit.ACTION_STAFF_DELETE,
				audit.ENTITY_TYPE_STAFF,
				existing.ID,
				existing.Name,
				errLog,
			)
		}
	}

	return err
}
