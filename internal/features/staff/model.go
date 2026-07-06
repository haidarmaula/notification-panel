package staff

import "time"

type Staff struct {
	ID        int64
	RoleID    int64
	RoleName  string
	Name      string
	Email     string
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}
