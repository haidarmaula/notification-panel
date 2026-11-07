package profile

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	"hello/internal/database/sqlc"
	"hello/internal/staffmutation"
)

var (
	ErrProfileNotFound    = errors.New("profile not found")
	ErrInvalidCredentials = errors.New("current password is incorrect")
	ErrEmailAlreadyUsed   = errors.New("email already used by another account")
	ErrPasswordTooLong    = errors.New("password must not exceed 72 bytes")
)

// UpdateProfileParams contains the profile fields editable by an authenticated user.
type UpdateProfileParams struct {
	ID          int64
	Name, Email string
}

// ProfileService reads profiles and locks the authenticated session for writes.
type ProfileService struct {
	queries *sqlc.Queries
	db      staffmutation.Transactor
}

func NewProfileService(q *sqlc.Queries, db staffmutation.Transactor) *ProfileService {
	return &ProfileService{queries: q, db: db}
}

func profileForStaff(ctx context.Context, q *sqlc.Queries, staff sqlc.StaffUser) (*Profile, error) {
	role, err := q.GetRoleByID(ctx, staff.RoleID)
	if err != nil {
		return nil, fmt.Errorf("get profile role: %w", err)
	}
	return &Profile{ID: staff.ID, RoleID: staff.RoleID, RoleName: role.Name, Name: staff.Name, Email: staff.Email, IsActive: staff.IsActive, CreatedAt: staff.CreatedAt.Time, UpdatedAt: staff.UpdatedAt.Time}, nil
}

// GetProfile resolves the account and its current role.
func (s *ProfileService) GetProfile(ctx context.Context, staffID int64) (*Profile, error) {
	staff, err := s.queries.GetStaffUserByID(ctx, staffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProfileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get profile: %w", err)
	}
	return profileForStaff(ctx, s.queries, staff)
}

// UpdateProfile writes only supplied profile fields. The statement cannot change account authority.
func (s *ProfileService) UpdateProfile(ctx context.Context, p UpdateProfileParams) (*Profile, error) {
	return staffmutation.Run(ctx, s.db, p.ID, false, func(q *sqlc.Queries) (*Profile, error) {
		row, err := q.GetStaffUserByID(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("get account for profile update: %w", err)
		}
		if (p.Name == "" || p.Name == row.Name) && (p.Email == "" || p.Email == row.Email) {
			return profileForStaff(ctx, q, row)
		}
		row, err = q.UpdateOwnStaffProfile(ctx, sqlc.UpdateOwnStaffProfileParams{ID: p.ID, Name: p.Name, Email: p.Email})
		if staffmutation.EmailConflict(err) {
			return nil, ErrEmailAlreadyUsed
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProfileNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("update profile: %w", err)
		}
		return profileForStaff(ctx, q, row)
	})
}

// UpdatePassword verifies and replaces the password while holding the account lock.
// The session generation advances in the same transaction as the new hash.
func (s *ProfileService) UpdatePassword(ctx context.Context, staffID int64, currentPassword, newPassword string) error {
	_, err := staffmutation.Run(ctx, s.db, staffID, false, func(q *sqlc.Queries) (struct{}, error) {
		row, err := q.GetStaffUserByID(ctx, staffID)
		if err != nil {
			return struct{}{}, fmt.Errorf("get account for password change: %w", err)
		}
		if bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(currentPassword)) != nil {
			return struct{}{}, ErrInvalidCredentials
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if errors.Is(err, bcrypt.ErrPasswordTooLong) {
			return struct{}{}, ErrPasswordTooLong
		}
		if err != nil {
			return struct{}{}, fmt.Errorf("hash password: %w", err)
		}
		return struct{}{}, q.UpdateStaffPassword(ctx, sqlc.UpdateStaffPasswordParams{ID: staffID, PasswordHash: string(hash)})
	})
	return err
}
