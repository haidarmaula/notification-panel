package token

import (
	"errors"
	"os"
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

var accessSecret = []byte(os.Getenv("ACCESS_SECRET"))
var refreshSecret = []byte(os.Getenv("REFRESH_SECRET"))

const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 24 * time.Hour
)

var ErrInvalidToken = errors.New("invalid token")

func GenerateAccessToken(userID int64, email string) (string, error) {
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

	return token.SignedString(accessSecret)
}

func GenerateRefreshToken(userID int64) (string, error) {
	claims := RefreshClaims{
		UserID: userID,
		Type:   "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(refreshTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString(refreshSecret)
}

func ParseAccessToken(tokenString string) (*AccessClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &AccessClaims{}, func(t *jwt.Token) (any, error) {
		return accessSecret, nil
	})

	claims, ok := token.Claims.(*AccessClaims)

	if err != nil || !token.Valid || !ok || claims.Type != "access" {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

func ParseRefreshToken(tokenString string) (*RefreshClaims, error) {
	parsedToken, err := jwt.ParseWithClaims(
		tokenString,
		&RefreshClaims{},
		func(t *jwt.Token) (any, error) {
			return refreshSecret, nil
		},
	)

	claims := parsedToken.Claims.(*RefreshClaims)

	if err != nil || !parsedToken.Valid || claims.Type != "refresh" {
		return nil, ErrInvalidToken
	}

	return claims, nil
}
