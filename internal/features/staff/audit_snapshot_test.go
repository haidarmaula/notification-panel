package staff

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAuditProjectionExcludesCredentialsAtSource(t *testing.T) {
	row := dummyStaffUser(2)
	row.PasswordHash = "fixture-secret-hash"
	snapshot := snapshotStaffRow(row)
	data, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NotContains(t, string(data), row.PasswordHash)
	require.NotContains(t, string(data), "Password")
	require.Equal(t, row.ID, snapshot.ID)
	require.Equal(t, row.RoleID, snapshot.RoleID)
	require.Equal(t, row.Name, snapshot.Name)
	require.Equal(t, row.Email, snapshot.Email)
	require.Equal(t, row.CreatedAt.Time, snapshot.CreatedAt)
	require.Equal(t, row.UpdatedAt.Time, snapshot.UpdatedAt)
	require.Equal(t, row.IsActive, snapshot.IsActive)
	domain := Staff{ID: row.ID, RoleID: row.RoleID, RoleName: "ADMIN", Name: row.Name, Email: row.Email, IsActive: row.IsActive, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	snapshot.RoleName = "ADMIN"
	require.Equal(t, snapshot, snapshotStaff(domain))
}
