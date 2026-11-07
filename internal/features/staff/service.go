package staff

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/staffmutation"

	"golang.org/x/crypto/bcrypt"
)

// Password policy bounds enforced by validatePasswordPolicy.
//
// The minimum is eight characters, matching the NIST SP 800-63B
// recommendation and the shortest length that is not trivially brute
// forced. The maximum is 72 bytes, the hard limit of the bcrypt input
// length; a longer password is either rejected by the library or
// silently truncated, both of which are worse than a clear rejection at
// the service boundary.
//
// The policy deliberately does not mandate character classes
// (uppercase, digit, symbol). NIST SP 800-63B recommends against
// composition rules because they encourage predictable substitutions
// (P@ssw0rd!) without measurably improving resistance to guessing. The
// policy is enforced at both the admin reset path (this file) and, where
// applicable, the self-service path in the profile feature.
const (
	// minPasswordLength is the shortest password the service accepts.
	minPasswordLength = 8

	// maxPasswordLength is the longest password the service accepts.
	// The bound is derived from bcrypt's hard input limit; a longer
	// value cannot be hashed without silent truncation.
	maxPasswordLength = 72
)

// Domain errors for staff service.
//
// Five sentinels are declared here. Two of them — ErrEmailAlreadyRegistered
// and ErrEmailAlreadyUsed — represent the same class of conflict (an email
// already in use) but are returned from different methods and have
// different behaviours at the handler boundary:
//
//   - ErrEmailAlreadyRegistered is returned by Create, and the handler's
//     Create method maps it to 409 Conflict.
//   - ErrEmailAlreadyUsed is returned by Update, and the handler's Update
//     method now maps it to 409 Conflict as well — the two sentinels
//     remain distinct to preserve the method-level audit trail, but the
//     handler treats them identically.
//
// The two should probably be a single sentinel; they are currently
// distinct because of the order in which the paths were written. A future
// consolidation would be safe as long as both handler switches continue
// to recognise the resulting value.
var (
	// ErrEmailAlreadyRegistered is returned by Create when the supplied
	// email already exists on another staff_users row. The check that
	// produces it — staffRepo.ExistsByEmail — is advisory, not atomic;
	// see Create for the concurrency caveat.
	//
	// The handler maps this to 409 Conflict.
	ErrEmailAlreadyRegistered = errors.New("email already registered")

	// ErrInvalidRole is returned by Create and Update when the supplied
	// role name does not resolve to a roles row. The comparison is
	// case-sensitive against the stored name.
	//
	// The handler maps this to 400 Bad Request.
	ErrInvalidRole = errors.New("invalid role")

	// ErrStaffNotFound is returned when the requested staff ID does not
	// resolve to a row. It is returned only when the repository reports
	// pgx.ErrNoRows; any other lookup failure is wrapped and surfaced as a
	// non-sentinel error so the handler can map it to 500 rather than 404.
	//
	// The handler maps this to 404 Not Found.
	ErrStaffNotFound = errors.New("staff user not found")

	// ErrEmailAlreadyUsed is returned by Update when the proposed new
	// email collides with another staff user's email. It represents the
	// same class of conflict as ErrEmailAlreadyRegistered, but it is
	// emitted from the Update path rather than the Create path.
	//
	// The handler's Update method maps it to 409 Conflict. The two
	// sentinels remain distinct for API-caller clarity even though the
	// HTTP response is identical to the Create conflict.
	ErrEmailAlreadyUsed = errors.New("email already used by another staff")

	// ErrCannotDeleteSelf forbids deleting the authenticated principal.
	ErrCannotDeleteSelf = errors.New("cannot delete your own account")

	// ErrForbidden aliases the shared current-authority permission denial.
	ErrForbidden = authz.ErrForbidden

	// ErrPasswordTooShort is returned by UpdatePassword when the supplied
	// new password is shorter than minPasswordLength. The length is
	// measured in bytes, which equals the character count for ASCII
	// input and overcounts for multi-byte UTF-8; the overcount is
	// intentional, since bcrypt's own limit is expressed in bytes.
	//
	// The handler maps this to 400 Bad Request.
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")

	// ErrPasswordTooLong is returned by UpdatePassword when the supplied
	// new password exceeds maxPasswordLength. The bound exists because
	// bcrypt either rejects or silently truncates inputs beyond 72
	// bytes; rejecting explicitly avoids a password that appears to be
	// stored in full but is only validated against its first 72 bytes.
	//
	// The handler maps this to 400 Bad Request.
	ErrPasswordTooLong = errors.New("password must not exceed 72 characters")

	// ErrInvalidCurrentPassword is returned by UpdatePassword when the
	// caller's CurrentPassword does not match the acting super-admin's
	// stored bcrypt hash.
	//
	// The sentinel implements the re-authentication ("sudo mode") step
	// described on UpdatePasswordRequest: possession of a valid
	// super-admin token is not sufficient to reset another account's
	// password; the caller must also demonstrate knowledge of the
	// password that would let them authenticate afresh. A stolen token
	// — for example, one exfiltrated from a browser session — is
	// therefore not on its own enough to take over an account.
	//
	// The sentinel is deliberately not collapsed into ErrForbidden. A
	// 403 with a distinct message tells the legitimate caller that the
	// token is valid but the confirmation password is wrong, which is
	// actionable; collapsing the two would leave the caller unable to
	// distinguish "your session is no longer authorised" from "you
	// mistyped your password".
	//
	// The handler maps this to 403 Forbidden.
	ErrInvalidCurrentPassword = errors.New("current password is incorrect")
)

// CreateStaffParams holds the input for creating a new staff user.
//
// It is the service-layer counterpart of CreateStaffRequest. The handler
// decodes the wire request and passes the fields through; the service
// validates email uniqueness, resolves the role name to an ID, hashes the
// password, and inserts the row.
//
// The struct is a plain data value, constructed on the stack by the
// handler and passed by value.
type CreateStaffParams struct {
	// Role is the role name to assign, not the numeric ID. The service
	// resolves it via RoleRepository.FindByName; a name that does not
	// match an existing roles row produces ErrInvalidRole.
	//
	// A caller that supplies "SUPER_ADMIN" is permitted to do so provided
	// the actor passes the staff.manage authority check; the service does not
	// impose any additional restriction on which roles a super admin may
	// assign.
	Role string

	// Name is the new staff user's display name.
	Name string

	// Email is the new staff user's login email. The service checks for
	// an existing row with the same email and rejects a collision with
	// ErrEmailAlreadyRegistered; the check is advisory — see Create.
	Email string

	// Password is the new staff user's plaintext password. The service
	// hashes it with bcrypt before storing; the plaintext value never
	// leaves the request scope and must not be logged.
	Password string
}

// UpdateStaffParams holds the input for updating a staff user.
//
// It is the service-layer counterpart of UpdateStaffRequest. All three
// mutable fields are plain strings, so the service cannot distinguish
// "field omitted" from "field set to the empty string". The empty string
// is used as the sentinel for "leave the existing value unchanged":
//
//   - Role == "" means the role is not changed.
//   - Name == "" means the name is not changed.
//   - Email == "" means the email is not changed.
//
// A caller that genuinely wants to set a field to empty cannot do so
// through this API. The handler enforces that at least one field is
// non-empty before invoking the service; the service does not re-check.
type UpdateStaffParams struct {
	// ID is the internal staff_users.id being updated.
	ID int64

	// Role is the new role name. An empty string means "leave unchanged".
	// A non-empty value must resolve to a roles row; otherwise the
	// service returns ErrInvalidRole.
	Role string

	// Name is the new display name. An empty string means "leave
	// unchanged".
	Name string

	// Email is the new login email. An empty string, or a value equal to
	// the current email, means "leave unchanged" — the existence check
	// is skipped for a same-value update, so a client that resends the
	// current email does not trigger a spurious ErrEmailAlreadyUsed.
	Email string
}

// StaffService manages staff with independent current-DB authority checks on
// every public operation. Checks are statement snapshots, not transaction locks.
// Password reset additionally re-authenticates the actor. Audits remain best effort.
type operations struct {
	permissions  authz.Checker
	staffRepo    StaffUserRepository
	roleRepo     RoleRepository
	auditService AuditService
}

// newOperations requires an explicit current-authority checker; no fallback exists.
func newOperations(staffRepo StaffUserRepository, roleRepo RoleRepository, auditService AuditService, permissions authz.Checker) *operations {
	if permissions == nil {
		panic("staff: authorization checker is required")
	}
	return &operations{staffRepo: staffRepo, roleRepo: roleRepo, auditService: auditService, permissions: permissions}
}

// passwordActor checks staff management permission before loading the actor's hash.
// Missing/inactive actors fail closed; infrastructure failures remain internal.
func (s *operations) passwordActor(ctx context.Context) (*sqlc.StaffUser, error) {
	principal, err := s.permissions.Require(ctx, authz.StaffManage)
	if err != nil {
		return nil, err
	}
	actor, err := s.staffRepo.FindByID(ctx, principal.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, authz.ErrUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("load password actor: %w", err)
	}
	if actor.ID != principal.ID {
		return nil, errors.New("inconsistent password actor identity")
	}
	if !actor.IsActive {
		return nil, authz.ErrUnauthenticated
	}
	return &actor, nil
}

// validatePasswordPolicy enforces the service's password-length policy.
//
// The function checks two bounds: a minimum of minPasswordLength and a
// maximum of maxPasswordLength. A password that falls outside either
// bound returns the corresponding sentinel — ErrPasswordTooShort or
// ErrPasswordTooLong — which the handler maps to 400 Bad Request.
//
// # What the policy does not check
//
// The function performs no character-class analysis. Composition rules
// (uppercase, digit, symbol) are deliberately not enforced; see the
// documentation on the policy constants for the rationale. Callers that
// require composition rules must apply them at the handler layer, in
// addition to this check.
//
// The function does not compare the new password to the old one. A
// caller that requires "must differ from the previous password" must
// implement that check against the stored hash itself; the current
// service does not.
//
// # Concurrency
//
// Safe for concurrent use. The function is pure: it reads only its
// parameter and returns a sentinel or nil.
func validatePasswordPolicy(password string) error {
	if len(password) < minPasswordLength {
		return ErrPasswordTooShort
	}
	if len(password) > maxPasswordLength {
		return ErrPasswordTooLong
	}
	return nil
}

// Create assigns a role and records a redacted create snapshot.
func (s *operations) Create(ctx context.Context, params CreateStaffParams) (*Staff, error) {
	if _, err := s.permissions.Require(ctx, authz.StaffManage); err != nil {
		return nil, err
	}

	exists, err := s.staffRepo.ExistsByEmail(ctx, params.Email)
	if err != nil {
		return nil, fmt.Errorf("check email: %w", err)
	}
	if exists {
		return nil, ErrEmailAlreadyRegistered
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
	if err != nil {
		if errors.Is(err, bcrypt.ErrPasswordTooLong) {
			return nil, ErrPasswordTooLong
		}
		return nil, fmt.Errorf("hash password: %w", err)
	}

	role, err := s.roleRepo.FindByName(ctx, params.Role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidRole
		}
		return nil, fmt.Errorf("get staff role: %w", err)
	}

	staff, err := s.staffRepo.Create(ctx, sqlc.CreateStaffUserParams{
		RoleID:       role.ID,
		Name:         params.Name,
		Email:        params.Email,
		PasswordHash: string(hashed),
	})
	if err != nil {
		if staffmutation.EmailConflict(err) {
			return nil, ErrEmailAlreadyRegistered
		}
		return nil, fmt.Errorf("create staff: %w", err)
	}

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_STAFF_CREATE,
		EntityType: audit.ENTITY_TYPE_STAFF,
		EntityName: staff.Name,
		EntityID:   staff.ID,
		After:      snapshotStaffRow(staff),
	}); errLog != nil {
		return nil, fmt.Errorf("write staff audit: %w", errLog)
	}

	return &Staff{
		ID:        staff.ID,
		RoleID:    staff.RoleID,
		RoleName:  role.Name,
		Name:      staff.Name,
		Email:     staff.Email,
		IsActive:  staff.IsActive,
		CreatedAt: staff.CreatedAt.Time,
		UpdatedAt: staff.UpdatedAt.Time,
	}, nil
}

// GetByID retrieves a staff user by ID along with their role name.
// Requires current-DB staff.manage before target reads or other business work.
// Authentication/permission failures use the shared authz errors.
// Returns ErrStaffNotFound if the user does not exist.
//
// # Flow
//
//  1. Load the staff row by ID. A missing row returns ErrStaffNotFound;
//     any other repository failure is wrapped with "get staff: " and
//     returned as a non-sentinel error.
//  2. Load the associated role to obtain the display name. A failure here
//     is wrapped with the prefix "get role: " and returned as a
//     non-sentinel error.
//  3. Return the domain Staff view.
//
// # Errors
//
// Returns ErrStaffNotFound only when the staff lookup returns
// pgx.ErrNoRows. Other staff-lookup failures are wrapped with
// "get staff: " and are not sentinels. A role-lookup failure is wrapped
// with "get role: " and served through the handler's default 500 branch,
// with only the generic "internal server error" disclosed to the client.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *operations) GetByID(ctx context.Context, id int64) (*Staff, error) {
	if _, err := s.permissions.Require(ctx, authz.StaffManage); err != nil {
		return nil, err
	}
	return s.getByID(ctx, id)
}

// getByID is only used after the owning public operation authorized the caller.
func (s *operations) getByID(ctx context.Context, id int64) (*Staff, error) {
	staff, err := s.staffRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrStaffNotFound
		}
		return nil, fmt.Errorf("get staff: %w", err)
	}

	role, err := s.roleRepo.FindByID(ctx, staff.RoleID)
	if err != nil {
		return nil, fmt.Errorf("get role: %w", err)
	}

	return &Staff{
		ID:        staff.ID,
		RoleID:    staff.RoleID,
		RoleName:  role.Name,
		Name:      staff.Name,
		Email:     staff.Email,
		IsActive:  staff.IsActive,
		CreatedAt: staff.CreatedAt.Time,
		UpdatedAt: staff.UpdatedAt.Time,
	}, nil
}

// List returns staff and a total using the same name and email filter.
func (s *operations) List(ctx context.Context, page, limit int32, search string) ([]Staff, int64, error) {
	if _, err := s.permissions.Require(ctx, authz.StaffManage); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * limit

	var staffs []Staff
	var total int64
	var err error

	if search != "" {
		rows, err := s.staffRepo.Search(ctx, search, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("search staff: %w", err)
		}

		staffs = make([]Staff, len(rows))
		for i, row := range rows {
			staffs[i] = Staff{
				ID:        row.ID,
				RoleID:    row.RoleID,
				RoleName:  row.RoleName,
				Name:      row.Name,
				Email:     row.Email,
				IsActive:  row.IsActive,
				CreatedAt: row.CreatedAt.Time,
				UpdatedAt: row.UpdatedAt.Time,
			}
		}
	} else {
		rows, err := s.staffRepo.List(ctx, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("list staff: %w", err)
		}

		staffs = make([]Staff, len(rows))
		for i, row := range rows {
			staffs[i] = Staff{
				ID:        row.ID,
				RoleID:    row.RoleID,
				RoleName:  row.RoleName,
				Name:      row.Name,
				Email:     row.Email,
				IsActive:  row.IsActive,
				CreatedAt: row.CreatedAt.Time,
				UpdatedAt: row.UpdatedAt.Time,
			}
		}
	}

	total, err = s.staffRepo.CountMatching(ctx, search)
	if err != nil {
		return nil, 0, fmt.Errorf("count staff: %w", err)
	}

	return staffs, total, nil
}

// Update applies partial fields to the locked target, then records its before and after snapshots.
func (s *operations) Update(ctx context.Context, params UpdateStaffParams) (*Staff, error) {
	if _, err := s.permissions.Require(ctx, authz.StaffManage); err != nil {
		return nil, err
	}
	existing, err := s.staffRepo.FindByID(ctx, params.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrStaffNotFound
		}
		return nil, fmt.Errorf("get staff for update: %w", err)
	}

	update := sqlc.UpdateStaffUserParams{
		ID:     params.ID,
		RoleID: existing.RoleID,
		Name:   existing.Name,
		Email:  existing.Email,
	}

	if params.Role != "" {
		role, err := s.roleRepo.FindByName(ctx, params.Role)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrInvalidRole
			}
			return nil, fmt.Errorf("get staff role: %w", err)
		}
		update.RoleID = role.ID
	}

	if params.Name != "" {
		update.Name = params.Name
	}

	if params.Email != "" && params.Email != existing.Email {
		exists, err := s.staffRepo.ExistsByEmail(ctx, params.Email)
		if err != nil {
			return nil, fmt.Errorf("check email: %w", err)
		}
		if exists {
			return nil, ErrEmailAlreadyUsed
		}
		update.Email = params.Email
	}

	if err := s.staffRepo.Update(ctx, update); err != nil {
		if staffmutation.EmailConflict(err) {
			return nil, ErrEmailAlreadyUsed
		}
		return nil, fmt.Errorf("update staff: %w", err)
	}

	staff, err := s.getByID(ctx, params.ID)
	if err == nil {
		if errLog := s.auditService.Log(ctx, audit.LogParams{
			Action:     audit.ACTION_STAFF_UPDATE,
			EntityType: audit.ENTITY_TYPE_STAFF,
			EntityName: staff.Name,
			EntityID:   staff.ID,
			Before:     snapshotStaffRow(existing),
			After:      snapshotStaff(*staff),
		}); errLog != nil {
			return nil, fmt.Errorf("write staff audit: %w", errLog)
		}
	}

	return staff, err
}

// UpdateStatus writes account activation and records its before and after snapshots.
func (s *operations) UpdateStatus(ctx context.Context, id int64, isActive bool) (*Staff, error) {
	if _, err := s.permissions.Require(ctx, authz.StaffManage); err != nil {
		return nil, err
	}
	existing, err := s.staffRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrStaffNotFound
		}
		return nil, fmt.Errorf("get staff for update status: %w", err)
	}

	if err := s.staffRepo.UpdateStatus(ctx, sqlc.UpdateStaffStatusParams{ID: id, IsActive: isActive}); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}

	staff, err := s.getByID(ctx, id)
	if err == nil {
		if errLog := s.auditService.Log(ctx, audit.LogParams{
			Action:     audit.ACTION_STAFF_UPDATE_STATUS,
			EntityType: audit.ENTITY_TYPE_STAFF,
			EntityName: staff.Name,
			EntityID:   staff.ID,
			Before:     snapshotStaffRow(existing),
			After:      snapshotStaff(*staff),
		}); errLog != nil {
			return nil, fmt.Errorf("write staff audit: %w", errLog)
		}
	}

	return staff, err
}

// UpdatePassword verifies the acting administrator's current password before resetting the locked target.
func (s *operations) UpdatePassword(ctx context.Context, id int64, currentPassword, newPassword string) error {
	actor, err := s.passwordActor(ctx)
	if err != nil {
		return err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(actor.PasswordHash), []byte(currentPassword)); err != nil {
		return ErrInvalidCurrentPassword
	}

	if err := validatePasswordPolicy(newPassword); err != nil {
		return err
	}

	existing, err := s.staffRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaffNotFound
		}
		return fmt.Errorf("get staff for update password: %w", err)
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	err = s.staffRepo.UpdatePassword(ctx, sqlc.UpdateStaffPasswordParams{
		ID:           id,
		PasswordHash: string(hashed),
	})

	if err == nil {
		if errLog := s.auditService.Log(ctx, audit.LogParams{
			Action:     audit.ACTION_STAFF_UPDATE_PASSWORD,
			EntityType: audit.ENTITY_TYPE_STAFF,
			EntityName: existing.Name,
			EntityID:   existing.ID,
		}); errLog != nil {
			return fmt.Errorf("write staff audit: %w", errLog)
		}
	}

	return err
}

// Delete requires another account as its target and records the last redacted snapshot.
func (s *operations) Delete(ctx context.Context, id int64) error {
	principal, err := s.permissions.Require(ctx, authz.StaffManage)
	if err != nil {
		return err
	}
	existing, err := s.staffRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaffNotFound
		}
		return fmt.Errorf("get staff for delete: %w", err)
	}

	if principal.ID == id {
		return ErrCannotDeleteSelf
	}

	err = s.staffRepo.Delete(ctx, id)
	if err == nil {
		if errLog := s.auditService.Log(ctx, audit.LogParams{
			Action:     audit.ACTION_STAFF_DELETE,
			EntityType: audit.ENTITY_TYPE_STAFF,
			EntityName: existing.Name,
			EntityID:   existing.ID,
			Before:     snapshotStaffRow(existing),
		}); errLog != nil {
			return fmt.Errorf("write staff audit: %w", errLog)
		}
	}

	return err
}
