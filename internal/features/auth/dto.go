// Package auth handles staff login, refresh rotation and current-family logout.
package auth

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
type LoginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// RefreshTokenResponse returns both successors. Clients must atomically replace their pair.
type RefreshTokenResponse = LoginResponse
