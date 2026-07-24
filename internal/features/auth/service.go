package auth

import (
	"context"
	"errors"

	"hello/internal/database/repository"
	"hello/internal/token"

	"golang.org/x/crypto/bcrypt"
)

// Authentication errors.
var (
	ErrInvalidCredentials     = errors.New("invalid email or password")
	ErrEmailAlreadyRegistered = errors.New("email already registered")
	ErrInvalidStaffRoleID     = errors.New("invalid staff role id")
)

// LoginResult holds the tokens returned after a successful login.
type LoginResult struct {
	AccessToken  string
	RefreshToken string
}

// AuthService handles staff authentication and token management.
type AuthService struct {
	staffRepo    *repository.StaffUserRepository
	roleRepo     *repository.RoleRepository
	tokenManager *token.TokenManager
}

// NewAuthService creates a new AuthService instance with the required dependencies.
func NewAuthService(
	staffRepo *repository.StaffUserRepository,
	roleRepo *repository.RoleRepository,
	tokenManager *token.TokenManager,
) *AuthService {
	return &AuthService{
		staffRepo:    staffRepo,
		roleRepo:     roleRepo,
		tokenManager: tokenManager,
	}
}

// Login authenticates a staff user by email and password.
// Returns access and refresh tokens on success, or ErrInvalidCredentials if
// the credentials are incorrect or the account is inactive.
func (s *AuthService) Login(
	ctx context.Context,
	email string,
	password string,
) (*LoginResult, error) {
	staff, err := s.staffRepo.FindByEmail(ctx, email)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	if !staff.IsActive {
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword(
		[]byte(staff.PasswordHash),
		[]byte(password),
	); err != nil {
		return nil, ErrInvalidCredentials
	}

	role, err := s.roleRepo.FindByID(ctx, staff.RoleID)
	if err != nil {
		return nil, ErrInvalidStaffRoleID
	}

	accessToken, err := s.tokenManager.GenerateAccessToken(
		staff.ID,
		role.Name,
		staff.Email,
	)
	if err != nil {
		return nil, err
	}

	refreshToken, err := s.tokenManager.GenerateRefreshToken(staff.ID, role.Name)
	if err != nil {
		return nil, err
	}

	return &LoginResult{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

// RefreshToken validates a refresh token and issues a new access token.
// Returns ErrInvalidCredentials if the token is invalid, expired, or the
// associated staff account is inactive.
func (s *AuthService) RefreshToken(
	ctx context.Context,
	refreshToken string,
) (*RefreshTokenResponse, error) {
	claims, err := s.tokenManager.ParseRefreshToken(refreshToken)
	if err != nil {
		return nil, token.ErrInvalidToken
	}

	staff, err := s.staffRepo.FindByID(ctx, claims.StaffID)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	if !staff.IsActive {
		return nil, ErrInvalidCredentials
	}

	role, err := s.roleRepo.FindByID(ctx, staff.ID)
	if err != nil {
		return nil, ErrInvalidStaffRoleID
	}

	accessToken, err := s.tokenManager.GenerateAccessToken(
		staff.ID,
		role.Name,
		staff.Email,
	)
	if err != nil {
		return nil, err
	}

	return &RefreshTokenResponse{
		AccessToken: accessToken,
	}, nil
}
