package auth

// LoginRequest represents the payload for staff login endpoint.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse contains the tokens returned after successful authentication.
type LoginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// RefreshTokenRequest carries the refresh token to obtain a new access token.
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// RefreshTokenResponse contains the new access token.
type RefreshTokenResponse struct {
	AccessToken string `json:"access_token"`
}
