package auth

import (
	"context"
	"crypto/sha256"
	"errors"

	"hello/internal/database/sqlc"
	"hello/internal/token"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidCredentials covers unknown, inactive, expired and revoked accounts or sessions.
var ErrInvalidCredentials = errors.New("invalid email or password")

// AuthService binds signed staff tokens to a current database session.
type AuthService struct {
	q      *sqlc.Queries
	tokens *token.TokenManager
}

func (s *AuthService) Login(ctx context.Context, email, password string) (*LoginResponse, error) {
	staff, err := s.q.GetStaffUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !staff.IsActive || len(password) > 72 || bcrypt.CompareHashAndPassword([]byte(staff.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	family := token.NewFamily()
	pair, err := s.issue(ctx, staff, family)
	if err != nil {
		return nil, err
	}
	added, err := s.q.OpenStaffSession(ctx, sqlc.OpenStaffSessionParams{SessionID: family.ID, StaffID: staff.ID, AuthEpoch: staff.AuthEpoch,
		RefreshDigest: pair.RefreshDigest[:], ExpiresAt: pgtype.Timestamptz{Time: family.ExpiresAt, Valid: true}})
	if err != nil {
		return nil, err
	}
	if added != 1 {
		return nil, ErrInvalidCredentials
	}
	return &LoginResponse{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken}, nil
}

func (s *AuthService) issue(ctx context.Context, staff sqlc.StaffUser, family token.Family) (token.Pair, error) {
	role, err := s.q.GetRoleByID(ctx, staff.RoleID)
	if err != nil {
		return token.Pair{}, err
	}
	return s.tokens.Issue(staff.ID, role.Name, staff.Email, family)
}

// RefreshToken rotates the stored refresh digest atomically. A reused token
// commits revocation before this method returns an authentication failure.
func (s *AuthService) RefreshToken(ctx context.Context, raw string) (*RefreshTokenResponse, error) {
	c, err := s.tokens.ParseRefreshToken(raw)
	if err != nil {
		return nil, token.ErrInvalidToken
	}
	staff, err := s.q.GetStaffUserByID(ctx, c.StaffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !staff.IsActive {
		return nil, ErrInvalidCredentials
	}
	family := token.Family{ExpiresAt: c.ExpiresAt.Time}
	if err := family.ID.Scan(c.SessionID); err != nil {
		return nil, token.ErrInvalidToken
	}
	pair, err := s.issue(ctx, staff, family)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(c.ID))
	rotated, err := s.q.RotateStaffSession(ctx, sqlc.RotateStaffSessionParams{SessionID: family.ID, StaffID: staff.ID, AuthEpoch: staff.AuthEpoch,
		ExpiresAt: pgtype.Timestamptz{Time: family.ExpiresAt, Valid: true}, PresentedDigest: hash[:], NextDigest: pair.RefreshDigest[:]})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !rotated {
		return nil, ErrInvalidCredentials
	}
	return &RefreshTokenResponse{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken}, nil
}

// Logout accepts a refresh credential and invalidates only the represented
// family. Repeating logout with an unexpired signed credential is idempotent.
func (s *AuthService) Logout(ctx context.Context, raw string) error {
	c, err := s.tokens.ParseRefreshToken(raw)
	if err != nil {
		return token.ErrInvalidToken
	}
	var id pgtype.UUID
	if err := id.Scan(c.SessionID); err != nil {
		return token.ErrInvalidToken
	}
	_, err = s.q.RevokeStaffSession(ctx, sqlc.RevokeStaffSessionParams{SessionID: id, StaffID: c.StaffID})
	return err
}
