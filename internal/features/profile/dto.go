package profile

// ProfileResponse represents the response payload for a staff profile.
type ProfileResponse struct {
	ID        int64  `json:"id"`
	RoleID    int64  `json:"role_id"`
	RoleName  string `json:"role_name"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	IsActive  bool   `json:"is_active"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// UpdateProfileRequest represents the request payload for updating a profile.
type UpdateProfileRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// UpdatePasswordRequest represents the request payload for updating password.
type UpdatePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}
