package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

var (
	ErrApplicationNotFound  = errors.New("application not found")
	ErrApplicationNotActive = errors.New("application is not active")
)

// ApplicationAuthorityRepository returns principal, optional app and membership
// in one statement. It may be bound to a pool or the owning transaction.
type ApplicationAuthorityRepository interface {
	GetApplicationAuthority(context.Context, sqlc.GetApplicationAuthorityParams) (sqlc.GetApplicationAuthorityRow, error)
}
type ApplicationPrincipal struct {
	Staff         Principal
	ApplicationID int64
	PublicID      pgtype.UUID
	Status        string
}
type ApplicationChecker interface {
	RequireApplication(context.Context, pgtype.UUID, Permission) (ApplicationPrincipal, error)
}
type ApplicationAuthorizer struct {
	repository ApplicationAuthorityRepository
}

func NewApplicationAuthorizer(repo ApplicationAuthorityRepository) *ApplicationAuthorizer {
	if repo == nil {
		panic("authz: application authority repository is required")
	}
	return &ApplicationAuthorizer{repository: repo}
}

// RequireApplication never treats SUPER_ADMIN or a JWT app/role claim as an app
// grant. Only metadata reads are allowed during PROVISIONING. Authority is a
// fresh statement snapshot, not a lock through a later operation.
func (a *ApplicationAuthorizer) RequireApplication(ctx context.Context, id pgtype.UUID, p Permission) (ApplicationPrincipal, error) {
	deny := ApplicationPrincipal{}
	if ctx == nil {
		return deny, ErrUnauthenticated
	}
	if err := ctx.Err(); err != nil {
		return deny, fmt.Errorf("authorize application: %w", err)
	}
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	if !ok || claims == nil || claims.StaffID <= 0 {
		return deny, ErrUnauthenticated
	}
	if !id.Valid {
		return deny, ErrApplicationNotFound
	}
	if a == nil || a.repository == nil {
		return deny, errors.New("application authority unavailable")
	}
	row, err := a.repository.GetApplicationAuthority(ctx, sqlc.GetApplicationAuthorityParams{StaffID: claims.StaffID, PublicID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return deny, ErrUnauthenticated
	}
	if err != nil {
		return deny, fmt.Errorf("load application authority: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return deny, fmt.Errorf("authorize application: %w", err)
	}
	if row.StaffID != claims.StaffID {
		return deny, errors.New("inconsistent application principal")
	}
	if !row.StaffIsActive {
		return deny, ErrUnauthenticated
	}
	if !row.RoleName.Valid {
		return deny, errors.New("missing application authority role")
	}
	if !Allowed(row.RoleName.String, ApplicationsRead) {
		return deny, ErrForbidden
	}
	if !row.ApplicationID.Valid {
		return deny, ErrApplicationNotFound
	}
	if row.ApplicationID.Int64 <= 0 || !row.ApplicationPublicID.Valid || row.ApplicationPublicID != id || !row.ApplicationStatus.Valid {
		return deny, errors.New("inconsistent application authority")
	}
	if !row.GrantIsActive.Valid || !row.GrantIsActive.Bool {
		return deny, ErrApplicationNotFound
	}
	if !applicationOperation(p) || !Allowed(row.RoleName.String, p) {
		return deny, ErrForbidden
	}
	if p != ApplicationsRead && row.ApplicationStatus.String != "ACTIVE" {
		return deny, ErrApplicationNotActive
	}
	// Unknown statuses cannot accidentally permit even metadata access.
	switch row.ApplicationStatus.String {
	case "PROVISIONING", "ACTIVE", "HELD", "RETIRED":
	default:
		return deny, errors.New("unknown application status")
	}
	return ApplicationPrincipal{Staff: Principal{ID: row.StaffID, Role: row.RoleName.String}, ApplicationID: row.ApplicationID.Int64, PublicID: id, Status: row.ApplicationStatus.String}, nil
}

// Control-plane staff/project administration cannot be delegated by an app scope.
func applicationOperation(p Permission) bool {
	switch p {
	case ApplicationsRead, UsersRead, NotificationsRead, NotificationsWrite, NotificationsSend, NotificationsSchedule, SegmentsRead, SegmentsWrite, MembersRead, MembersWrite:
		return true
	default:
		return false
	}
}
