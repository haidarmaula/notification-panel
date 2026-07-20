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
)

// LoginResult holds the tokens returned after a successful login.
type LoginResult struct {
	AccessToken  string
	RefreshToken string
}

// AuthService handles staff authentication and token management.
type AuthService struct {
	repo         *repository.StaffUserRepository
	tokenManager *token.TokenManager
}

// NewAuthService creates a new AuthService instance with the required dependencies.
func NewAuthService(
	repo *repository.StaffUserRepository,
	tokenManager *token.TokenManager,
) *AuthService {
	return &AuthService{
		repo:         repo,
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
	admin, err := s.repo.FindByEmail(ctx, email)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	if !admin.IsActive {
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword(
		[]byte(admin.PasswordHash),
		[]byte(password),
	); err != nil {
		return nil, ErrInvalidCredentials
	}

	accessToken, err := s.tokenManager.GenerateAccessToken(
		admin.ID,
		admin.RoleID,
		admin.Email,
	)
	if err != nil {
		return nil, err
	}

	refreshToken, err := s.tokenManager.GenerateRefreshToken(admin.ID, admin.RoleID)
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

	admin, err := s.repo.FindByID(ctx, claims.StaffID)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	if !admin.IsActive {
		return nil, ErrInvalidCredentials
	}

	accessToken, err := s.tokenManager.GenerateAccessToken(
		admin.ID,
		admin.RoleID,
		admin.Email,
	)
	if err != nil {
		return nil, err
	}

	return &RefreshTokenResponse{
		AccessToken: accessToken,
	}, nil
}
