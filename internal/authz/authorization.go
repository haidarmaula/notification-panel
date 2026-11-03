// Package authz checks staff operations against current database authority.
// Checks use a statement snapshot, not a lock through the subsequent mutation;
// transactional revocation/audit guarantees belong to the mutation owner.
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

// Permission identifies a server-owned operation; unknown values deny.
type Permission string

const (
	UsersRead             Permission = "users.read"
	ApplicationsRead      Permission = "applications.read"
	ApplicationsManage    Permission = "applications.manage"
	StaffManage           Permission = "staff.manage"
	NotificationsRead     Permission = "notifications.read"
	NotificationsWrite    Permission = "notifications.draft.write"
	NotificationsSend     Permission = "notifications.send"
	NotificationsSchedule Permission = "notifications.schedule"
	SegmentsRead          Permission = "segments.read"
	SegmentsWrite         Permission = "segments.write"
	MembersRead           Permission = "segments.members.read"
	MembersWrite          Permission = "segments.members.write"
)

var (
	ErrUnauthenticated = errors.New("authentication required")
	ErrForbidden       = errors.New("insufficient permissions")
)

var grants = map[Permission][]string{
	UsersRead:             {"VIEWER", "OPERATOR", "ADMIN", "SUPER_ADMIN"},
	ApplicationsRead:      {"VIEWER", "OPERATOR", "ADMIN", "SUPER_ADMIN"},
	ApplicationsManage:    {"SUPER_ADMIN"},
	StaffManage:           {"SUPER_ADMIN"},
	NotificationsRead:     {"VIEWER", "OPERATOR", "ADMIN", "SUPER_ADMIN"},
	NotificationsWrite:    {"OPERATOR", "ADMIN", "SUPER_ADMIN"},
	NotificationsSend:     {"ADMIN", "SUPER_ADMIN"},
	NotificationsSchedule: {"ADMIN", "SUPER_ADMIN"},
	SegmentsRead:          {"VIEWER", "OPERATOR", "ADMIN", "SUPER_ADMIN"},
	SegmentsWrite:         {"OPERATOR", "ADMIN", "SUPER_ADMIN"},
	MembersRead:           {"VIEWER", "OPERATOR", "ADMIN", "SUPER_ADMIN"},
	MembersWrite:          {"OPERATOR", "ADMIN", "SUPER_ADMIN"},
}

// Allowed is case-sensitive and denies unknown roles/operations. It does not
// establish identity; callers must use Require for request authorization.
func Allowed(role string, permission Permission) bool {
	for _, candidate := range grants[permission] {
		if role == candidate {
			return true
		}
	}
	return false
}

// Principal carries the minimal current authority, never a public profile.
type Principal struct {
	ID   int64
	Role string
}

// AuthorityRepository may be bound to sqlc queries on a pool or transaction.
// GetStaffAuthority must read ID/active/role together, without password data.
type AuthorityRepository interface {
	GetStaffAuthority(context.Context, int64) (sqlc.GetStaffAuthorityRow, error)
}

// Checker is required by each service and route; all requested grants are ANDed.
type Checker interface {
	Require(context.Context, ...Permission) (Principal, error)
}

// Authorizer is immutable after construction and safe for concurrent checks
// when its underlying repository is concurrency-safe (as the pool binding is).
// A transaction binding inherits that transaction's concurrency restrictions.
type Authorizer struct{ repository AuthorityRepository }

// New requires an authority repository; it provides no fallback permissions.
func New(repository AuthorityRepository) *Authorizer {
	if repository == nil {
		panic("authz: authority repository is required")
	}
	return &Authorizer{repository: repository}
}

// Require re-reads live authority on every call. Token role claims and previous
// route checks are never cached or trusted. All permissions are required (AND).
func (a *Authorizer) Require(ctx context.Context, permissions ...Permission) (Principal, error) {
	if ctx == nil {
		return Principal{}, ErrUnauthenticated
	}
	if err := ctx.Err(); err != nil {
		return Principal{}, fmt.Errorf("authorize: %w", err)
	}
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	if !ok || claims == nil || claims.StaffID <= 0 {
		return Principal{}, ErrUnauthenticated
	}
	if a == nil || a.repository == nil {
		return Principal{}, errors.New("authz: authority unavailable")
	}
	row, err := a.repository.GetStaffAuthority(ctx, claims.StaffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("load staff authority: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Principal{}, fmt.Errorf("authorize: %w", err)
	}
	if row.ID != claims.StaffID {
		return Principal{}, errors.New("authz: inconsistent authority identity")
	}
	if !row.IsActive {
		return Principal{}, ErrUnauthenticated
	}
	if !row.RoleName.Valid {
		return Principal{}, errors.New("authz: missing authority role")
	}
	if len(permissions) == 0 {
		return Principal{}, ErrForbidden
	}
	for _, p := range permissions {
		if !Allowed(row.RoleName.String, p) {
			return Principal{}, ErrForbidden
		}
	}
	return Principal{ID: row.ID, Role: row.RoleName.String}, nil
}
