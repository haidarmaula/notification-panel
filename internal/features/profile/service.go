package profile

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
)

// Domain errors.
var (
	ErrProfileNotFound    = errors.New("profile not found")
	ErrInvalidCredentials = errors.New("current password is incorrect")
	ErrEmailAlreadyUsed   = errors.New("email already used by another account")
)

// UpdateProfileParams holds input for updating profile.
type UpdateProfileParams struct {
	ID    int64
	Name  string
	Email string
}

// ProfileService provides business logic for staff profile management.
type ProfileService struct {
	staffRepo *repository.StaffUserRepository
	roleRepo  *repository.RoleRepository
}

// NewProfileService creates a new ProfileService instance.
func NewProfileService(staffRepo *repository.StaffUserRepository, roleRepo *repository.RoleRepository) *ProfileService {
	return &ProfileService{
		staffRepo: staffRepo,
		roleRepo:  roleRepo,
	}
}

// GetProfile retrieves the profile of the authenticated staff user.
func (s *ProfileService) GetProfile(ctx context.Context, staffID int64) (*Profile, error) {
	staff, err := s.staffRepo.FindByID(ctx, staffID)
	if err != nil {
		return nil, ErrProfileNotFound
	}

	role, err := s.roleRepo.FindByID(ctx, staff.RoleID)
	if err != nil {
		return nil, fmt.Errorf("get role: %w", err)
	}

	return &Profile{
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

// UpdateProfile updates the name and/or email of the authenticated staff user.
func (s *ProfileService) UpdateProfile(ctx context.Context, params UpdateProfileParams) (*Profile, error) {
	existing, err := s.staffRepo.FindByID(ctx, params.ID)
	if err != nil {
		return nil, ErrProfileNotFound
	}

	update := sqlc.UpdateStaffUserParams{
		ID:     params.ID,
		RoleID: existing.RoleID,
		Name:   existing.Name,
		Email:  existing.Email,
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

	// Only perform update if there are changes
	if update.Name == existing.Name && update.Email == existing.Email {
		return s.GetProfile(ctx, params.ID)
	}

	if err := s.staffRepo.Update(ctx, update); err != nil {
		return nil, fmt.Errorf("update profile: %w", err)
	}

	return s.GetProfile(ctx, params.ID)
}

// UpdatePassword changes the password for the authenticated staff user.
func (s *ProfileService) UpdatePassword(ctx context.Context, staffID int64, currentPassword, newPassword string) error {
	staff, err := s.staffRepo.FindByID(ctx, staffID)
	if err != nil {
		return ErrProfileNotFound
	}

	// Verify current password
	if err := bcrypt.CompareHashAndPassword([]byte(staff.PasswordHash), []byte(currentPassword)); err != nil {
		return ErrInvalidCredentials
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	if err := s.staffRepo.UpdatePassword(ctx, sqlc.UpdateStaffPasswordParams{
		ID:           staffID,
		PasswordHash: string(hashed),
	}); err != nil {
		return fmt.Errorf("update password: %w", err)
	}

	return nil
}
