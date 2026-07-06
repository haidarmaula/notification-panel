package staff

// StaffResponse represents the response payload for a single staff user.
type StaffResponse struct {
	ID        int64  `json:"id"`
	RoleID    int64  `json:"role_id"`
	RoleName  string `json:"role_name"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	IsActive  bool   `json:"is_active"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ListStaffResponse represents the paginated list response for staff users.
type ListStaffResponse struct {
	Data       []StaffResponse `json:"data"`
	Total      int64           `json:"total"`
	Page       int32           `json:"page"`
	Limit      int32           `json:"limit"`
	TotalPages int32           `json:"total_pages"`
}

// CreateStaffRequest represents the request payload for creating a new staff user.
type CreateStaffRequest struct {
	Role     string `json:"role"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// UpdateStaffRequest represents the request payload for updating a staff user.
type UpdateStaffRequest struct {
	Role  string `json:"role"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// UpdateStatusRequest represents the request payload for updating staff active status.
type UpdateStatusRequest struct {
	IsActive bool `json:"is_active"`
}

// UpdatePasswordRequest represents the request payload for updating staff password.
type UpdatePasswordRequest struct {
	Password string `json:"password"`
}
