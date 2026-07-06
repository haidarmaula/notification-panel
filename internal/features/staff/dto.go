package staff

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

type ListStaffResponse struct {
	Data       []StaffResponse `json:"data"`
	Total      int64           `json:"total"`
	Page       int32           `json:"page"`
	Limit      int32           `json:"limit"`
	TotalPages int32           `json:"total_pages"`
}

type CreateStaffRequest struct {
	Role     string `json:"role"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type UpdateStaffRequest struct {
	Role  string `json:"role"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type UpdateStatusRequest struct {
	IsActive bool `json:"is_active"`
}

type UpdatePasswordRequest struct {
	Password string `json:"password"`
}
