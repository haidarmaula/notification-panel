package token

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AccessClaims struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Type   string `json:"type"`

	jwt.RegisteredClaims
}

type RefreshClaims struct {
	UserID int64  `json:"user_id"`
	Type   string `json:"type"`

	jwt.RegisteredClaims
}

const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 24 * time.Hour
)

var ErrInvalidToken = errors.New("invalid token")

type TokenManager struct {
	accessSecret  []byte
	refreshSecret []byte
}

func NewTokenManager(accessSecret, refreshSecret string) *TokenManager {
	return &TokenManager{
		accessSecret:  []byte(accessSecret),
		refreshSecret: []byte(refreshSecret),
	}
}

func (t *TokenManager) GenerateAccessToken(userID int64, email string) (string, error) {
	claims := AccessClaims{
		UserID: userID,
		Email:  email,
		Type:   "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(accessTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString(t.accessSecret)
}

func (t *TokenManager) GenerateRefreshToken(userID int64) (string, error) {
	claims := RefreshClaims{
		UserID: userID,
		Type:   "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(refreshTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString(t.refreshSecret)
}

func (t *TokenManager) ParseAccessToken(tokenString string) (*AccessClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &AccessClaims{}, func(_ *jwt.Token) (any, error) {
		return t.accessSecret, nil
	})

	claims, ok := token.Claims.(*AccessClaims)

	if err != nil || !token.Valid || !ok || claims.Type != "access" {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

func (t *TokenManager) ParseRefreshToken(tokenString string) (*RefreshClaims, error) {
	parsedToken, err := jwt.ParseWithClaims(
		tokenString,
		&RefreshClaims{},
		func(_ *jwt.Token) (any, error) {
			return t.refreshSecret, nil
		},
	)

	claims := parsedToken.Claims.(*RefreshClaims)

	if err != nil || !parsedToken.Valid || claims.Type != "refresh" {
		return nil, ErrInvalidToken
	}

	return claims, nil
}
