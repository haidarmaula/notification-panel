package auth

import (
	"context"
	"errors"

	"hello/internal/database/repository"
	"hello/internal/token"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials     = errors.New("invalid email or password")
	ErrEmailAlreadyRegistered = errors.New("email already registered")
)

type LoginResult struct {
	AccessToken  string
	RefreshToken string
}

type AuthService struct {
	repo         *repository.StaffUserRepository
	tokenManager *token.TokenManager
}

func NewAuthService(
	repo *repository.StaffUserRepository,
	tokenManager *token.TokenManager,
) *AuthService {
	return &AuthService{
		repo:         repo,
		tokenManager: tokenManager,
	}
}

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
