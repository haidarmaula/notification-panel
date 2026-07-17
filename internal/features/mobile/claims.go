package mobile

import "github.com/golang-jwt/jwt/v5"

// MobileClaims represents the JWT claims from Laravel backend.
type MobileClaims struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	jwt.RegisteredClaims
}
