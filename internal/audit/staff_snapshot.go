package audit

import (
	"encoding/json"
	"errors"
	"time"
)

// StaffSnapshot is the credential-free allowlist for staff audit state. Keep
// legacy Go-style JSON keys; adding fields requires an audit contract review.
// Name/email are intentional audit PII, not anonymized data.
type StaffSnapshot struct {
	ID        int64
	RoleID    int64
	RoleName  string `json:"RoleName,omitempty"`
	Name      string
	Email     string
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrUnsafeStaffSnapshot deliberately excludes supplied values and type details.
var ErrUnsafeStaffSnapshot = errors.New("staff audit requires a safe snapshot")

func marshalSnapshot(entityType string, value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	if entityType == ENTITY_TYPE_STAFF {
		switch snapshot := value.(type) {
		case StaffSnapshot:
		case *StaffSnapshot:
			if snapshot == nil {
				return nil, nil
			}
		default:
			return nil, ErrUnsafeStaffSnapshot
		}
	}
	return json.Marshal(value)
}
