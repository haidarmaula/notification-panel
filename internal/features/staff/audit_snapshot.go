package staff

import (
	"hello/internal/audit"
	"hello/internal/database/sqlc"
)

// snapshotStaffRow projects a database row before it crosses the audit boundary.
// PasswordHash and future repository fields are excluded by construction.
func snapshotStaffRow(row sqlc.StaffUser) audit.StaffSnapshot {
	return audit.StaffSnapshot{ID: row.ID, RoleID: row.RoleID, Name: row.Name, Email: row.Email,
		IsActive: row.IsActive, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}

func snapshotStaff(row Staff) audit.StaffSnapshot {
	return audit.StaffSnapshot{ID: row.ID, RoleID: row.RoleID, RoleName: row.RoleName,
		Name: row.Name, Email: row.Email, IsActive: row.IsActive, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
