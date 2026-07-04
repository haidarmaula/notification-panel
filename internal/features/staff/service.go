package staff

import (
	"context"
	"errors"
	"time"

	"hello/internal/database/repository"
	"hello/internal/database/sqlc"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrEmailAlreadyRegistered = errors.New("email already registered")
	ErrFailedToCreateAccount  = errors.New("failed to create account")
	ErrInvalidRole            = errors.New("invalid role")
)

type CreateStaffUserParams struct {
	Role     string
	Name     string
	Email    string
	Password string
}

type CreateStaffUserResult struct {
	ID        int64
	RoleID    int64
	Name      string
	Email     string
	IsActive  bool
	CreatedAt time.Time
}

type StaffService struct {
	staffRepo *repository.StaffRepository
	roleRepo  *repository.RoleRepository
}

func NewStaffService(staffRepo *repository.StaffRepository, roleRepo *repository.RoleRepository) *StaffService {
	return &StaffService{
		staffRepo: staffRepo,
		roleRepo:  roleRepo,
	}
}

func (s *StaffService) Create(ctx context.Context, params CreateStaffUserParams) (*CreateStaffUserResult, error) {
	_, err := s.staffRepo.FindByEmail(ctx, params.Email)
	if err == nil {
		return nil, ErrEmailAlreadyRegistered
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, ErrFailedToCreateAccount
	}

	roleEntity, err := s.roleRepo.FindByName(ctx, params.Role)
	if err != nil {
		return nil, ErrInvalidRole
	}

	staff, err := s.staffRepo.Create(ctx, sqlc.CreateStaffUserParams{
		RoleID:       roleEntity.ID,
		Name:         params.Name,
		Email:        params.Email,
		PasswordHash: string(hashed),
	})
	if err != nil {
		return nil, ErrFailedToCreateAccount
	}

	return &CreateStaffUserResult{
		ID:        staff.ID,
		RoleID:    staff.RoleID,
		Name:      staff.Name,
		Email:     staff.Email,
		IsActive:  staff.IsActive,
		CreatedAt: staff.CreatedAt.Time,
	}, nil
}
