package staff

import (
	"context"
	"errors"

	"hello/internal/database/repository"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrEmailAlreadyRegistered = errors.New("email already registered")
	ErrFailedToCreateAccount  = errors.New("failed to create account")
)

type CreateResult struct {
	RoleID int64
	Name   string
	Email  string
}

type StaffService struct {
	repo *repository.StaffRepository
}

func NewStaffService(repo *repository.StaffRepository) *StaffService {
	return &StaffService{
		repo: repo,
	}
}

func (s *StaffService) Create(ctx context.Context, roleID int64, name string, email string, password string) (*CreateResult, error) {
	_, err := s.repo.FindByEmail(ctx, email)
	if err == nil {
		return nil, ErrEmailAlreadyRegistered
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, ErrFailedToCreateAccount
	}

	staff, err := s.repo.Create(ctx, roleID, name, email, string(hashed))
	if err != nil {
		return nil, ErrFailedToCreateAccount
	}

	return &CreateResult{
		RoleID: staff.RoleID,
		Name:   staff.Name,
		Email:  staff.Email,
	}, nil
}
