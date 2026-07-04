package staff

import "time"

type CreateStaffUserRequest struct {
	Role     string `json:"role"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type CreateStaffUserResponse struct {
	ID        int64     `json:"id"`
	RoleID    int64     `json:"role_id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}
