package profile

import "time"

// Profile represents the domain model for a staff user profile.
type Profile struct {
	ID        int64
	RoleID    int64
	RoleName  string
	Name      string
	Email     string
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}
