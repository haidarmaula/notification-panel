package staff

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"

	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/database/sqlc"

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
type StaffService struct {
	permissions  authz.Checker
	staffRepo    StaffUserRepository
	roleRepo     RoleRepository
	auditService AuditService
}

// NewStaffService requires an explicit current-authority checker; no fallback exists.
func NewStaffService(staffRepo StaffUserRepository, roleRepo RoleRepository, auditService AuditService, permissions authz.Checker) *StaffService {
	if permissions == nil {
		panic("staff: authorization checker is required")
	}
	return &StaffService{staffRepo: staffRepo, roleRepo: roleRepo, auditService: auditService, permissions: permissions}
}

// passwordActor checks staff management permission before loading the actor's hash.
// Missing/inactive actors fail closed; infrastructure failures remain internal.
func (s *StaffService) passwordActor(ctx context.Context) (*sqlc.StaffUser, error) {
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

// Create creates a new staff user.
// Requires current-DB staff.manage before business work. Authentication and
// permission failures use shared authz errors.
// It validates email uniqueness, hashes the password, and assigns a role.
// Returns the created Staff object or an error.
// Possible errors: ErrForbidden, ErrEmailAlreadyRegistered, ErrInvalidRole.
//
// # Flow
//
//  0. Verify that the acting staff user holds the super-admin role via
//     the current-DB staff.manage checker before any business work. Missing
//     identity returns authz.ErrUnauthenticated; insufficient role returns
//     ErrForbidden. See authz.Authorizer.Require for the failure taxonomy.
//  1. Check for an existing staff user with the same email via
//     staffRepo.ExistsByEmail. A match returns ErrEmailAlreadyRegistered.
//  2. Hash params.Password with bcrypt at bcrypt.DefaultCost. A hashing
//     failure (rare — typically only for passwords exceeding bcrypt's
//     72-byte limit) is wrapped with "hash password: " and returned.
//  3. Resolve params.Role to a roles row via roleRepo.FindByName. A
//     not-found error collapses to ErrInvalidRole; other errors are
//     *also* collapsed to ErrInvalidRole, so a transient database failure
//     during the role lookup is reported to the client as "invalid role".
//  4. Insert the staff row with the resolved role ID and the bcrypt
//     hash.
//  5. Write an audit entry. An audit failure is logged and does not
//     abort the operation.
//  6. Return the domain Staff view of the created row.
//
// # Caller authorisation
//
// The super-admin invariant is enforced at step 0, independently of the
// HTTP layer. The service does not, however, restrict which role the
// caller may assign: a super admin may legitimately create another super
// admin, and the method does not distinguish between that case and the
// creation of a lower-privileged account. The privilege boundary
// protected by this method is therefore "staff management as a whole",
// not "the SUPER_ADMIN role in particular".
//
// # Password policy is not applied here
//
// Unlike UpdatePassword, this method does not call
// validatePasswordPolicy before hashing. A caller that supplies a
// one-character password to Create will have it accepted and stored.
// The asymmetry is a known gap; the fix would be a one-line call to
// validatePasswordPolicy alongside the existing step 0 check, but the
// change is scoped to C7 (which concerns UpdatePassword) and is left
// for a future revision.
//
// # Advisory email-uniqueness check
//
// The check at step 1 and the insert at step 4 are not atomic. Two
// concurrent Creates with the same email will both observe "not taken"
// and both attempt the insert; the outcome depends on whether the schema
// enforces a UNIQUE constraint on staff_users.email. If it does, the
// second insert fails at the database with a unique-violation error,
// wrapped with "create staff: " and served as a 500 by the handler —
// not the 409 that ErrEmailAlreadyRegistered would have produced. If the
// schema does not enforce uniqueness, both inserts succeed and the table
// contains two staff users sharing an email.
//
// # Password handling
//
// The plaintext password is read from params and hashed in this function;
// it is not stored, logged, or passed anywhere outside the hashing call.
// The bcrypt hash is the value written to the row. The Staff domain model
// returned by this method does not carry the hash — see model.go — so the
// response is safe to serialise.
//
// # Errors
//
// Returns ErrForbidden when the current DB role lacks permission, and
// authz.ErrUnauthenticated for missing/deleted/inactive identity. Returns
// ErrEmailAlreadyRegistered or ErrInvalidRole for the corresponding
// conditions. Returns a wrapped error for the actor-lookup, role-lookup,
// email-check, password-hash, or insert steps; none of the wrapped
// errors is a sentinel, so the handler serves them through its default
// 500 branch.
//
// # Concurrency
//
// Safe for concurrent use. See the advisory-check note above for the
// concurrency implications of the email check.
func (s *StaffService) Create(ctx context.Context, params CreateStaffParams) (*Staff, error) {
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
		return nil, fmt.Errorf("hash password: %w", err)
	}

	role, err := s.roleRepo.FindByName(ctx, params.Role)
	if err != nil {
		return nil, ErrInvalidRole
	}

	staff, err := s.staffRepo.Create(ctx, sqlc.CreateStaffUserParams{
		RoleID:       role.ID,
		Name:         params.Name,
		Email:        params.Email,
		PasswordHash: string(hashed),
	})
	if err != nil {
		return nil, fmt.Errorf("create staff: %w", err)
	}

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_STAFF_CREATE,
		EntityType: audit.ENTITY_TYPE_STAFF,
		EntityName: staff.Name,
		EntityID:   staff.ID,
		After:      snapshotStaffRow(staff),
	}); errLog != nil {
		log.Printf(
			"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
			audit.ACTION_STAFF_CREATE,
			audit.ENTITY_TYPE_STAFF,
			staff.ID,
			staff.Name,
			errLog,
		)
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
func (s *StaffService) GetByID(ctx context.Context, id int64) (*Staff, error) {
	if _, err := s.permissions.Require(ctx, authz.StaffManage); err != nil {
		return nil, err
	}
	return s.getByID(ctx, id)
}

// getByID is only used after the owning public operation authorized the caller.
func (s *StaffService) getByID(ctx context.Context, id int64) (*Staff, error) {
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

// List returns a paginated list of staff users with optional search by name or email.
// Requires current-DB staff.manage before target reads or other business work.
// Authentication/permission failures use the shared authz errors.
// Returns the list of Staff objects, total count, and any error.
//
// # Flow
//
// The method takes one of two branches depending on whether search is
// empty. Both branches fetch a page of joined staff/role rows and map them
// to domain Staff values; the difference is which repository method is
// used.
//
//   - When search is empty, the unfiltered List query is used.
//   - When search is non-empty, the Search query is used.
//
// After the branch, the total is obtained via staffRepo.Count.
//
// # Incorrect total on the search path
//
// The returned total is computed via staffRepo.Count(ctx) — the
// *unfiltered* row count — regardless of whether a search was applied.
// A caller searching for a rare name will receive a page of 0 or 1 items
// together with a total that reflects the entire staff_users table. A
// client building "page X of Y" pagination from these two values will
// render incorrect page counts on any filtered list. This is the same
// defect as the segments feature's `List`; the correct total on the
// search path would require a search-aware count query, which no
// repository method currently provides.
//
// # Errors
//
// Returns a wrapped error with the prefix "search staff: ", "list staff:
// ", or "count staff: " for failures of the corresponding repository
// call. None is a sentinel; the handler serves them through its default
// 500 branch.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *StaffService) List(ctx context.Context, page, limit int32, search string) ([]Staff, int64, error) {
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

	total, err = s.staffRepo.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count staff: %w", err)
	}

	return staffs, total, nil
}

// Update updates a staff user's role, name, or email.
// Requires current-DB staff.manage before target reads or other business work.
// Authentication/permission failures use the shared authz errors.
// Returns the updated Staff object or an error.
// Possible errors: ErrStaffNotFound, ErrInvalidRole, ErrEmailAlreadyUsed.
//
// # Flow
//
//  1. Load the existing staff row. A missing row returns
//     ErrStaffNotFound; any other repository failure is wrapped with
//     "get staff for update: " and returned as a non-sentinel error.
//  2. Build an update struct pre-populated with the existing values, so
//     that fields not supplied by the caller retain their current state.
//  3. If params.Role is non-empty, resolve it via roleRepo.FindByName. A
//     not-found error collapses to ErrInvalidRole (as does any other
//     error from that call).
//  4. If params.Name is non-empty, apply it.
//  5. If params.Email is non-empty AND differs from the existing email,
//     check for a collision via ExistsByEmail. A collision returns
//     ErrEmailAlreadyUsed.
//  6. Issue the UPDATE.
//  7. Re-fetch via private getByID to obtain the post-update state — a role
//     lookup is performed as part of this re-fetch, which adds a round
//     trip and introduces a failure mode (see below).
//  8. Write an audit entry recording the before and after snapshots. An
//     audit failure is logged and does not abort the operation.
//  9. Return the updated Staff.
//
// # Re-fetch failure produces a 500 after the write landed
//
// Step 7 re-reads the row (and its role) after the UPDATE has committed.
// If either read fails — a transient database error, a role-lookup
// failure — the method returns a wrapped or sentinel error and the
// handler serves a 500, even though the write at step 6 has already
// landed. A client that treats the 500 as "the update failed" will retry
// against a row that already reflects the requested change.
//
// The audit entry is written only if the re-fetch succeeds; a re-fetch
// failure therefore also drops the audit record, leaving the change
// unrecorded in the audit trail.
//
// # Advisory email-uniqueness check
//
// Same TOCTOU class as Create: the ExistsByEmail check at step 5 and the
// UPDATE at step 6 are not atomic. Two concurrent Updates claiming the
// same new email — or a concurrent Update and Create — can both pass the
// check; the outcome depends on the schema's unique constraint. If the
// constraint exists, the second write fails with a unique-violation
// wrapped as "update staff: " and served as 500; if not, two staff users
// end up sharing an email.
//
// # Errors
//
// Returns ErrStaffNotFound only when the existing staff row is absent
// (pgx.ErrNoRows), ErrInvalidRole for an unknown role, or
// ErrEmailAlreadyUsed for an email collision. Other lookup,
// uniqueness-check, update, and re-fetch failures are wrapped; the
// handler maps those to 500 and does not disclose internal details.
//
// # Concurrency
//
// Safe for concurrent use, but two concurrent Updates on the same staff
// row are not serialised. The final state reflects whichever update
// commits last.
func (s *StaffService) Update(ctx context.Context, params UpdateStaffParams) (*Staff, error) {
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
			return nil, ErrInvalidRole
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
			log.Printf(
				"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
				audit.ACTION_STAFF_UPDATE,
				audit.ENTITY_TYPE_STAFF,
				staff.ID,
				staff.Name,
				errLog,
			)
		}
	}

	return staff, err
}

// UpdateStatus changes the active status of a staff user.
// Requires current-DB staff.manage before target reads or other business work.
// Authentication/permission failures use the shared authz errors.
// Returns the updated Staff object or an error.
// Possible error: ErrStaffNotFound.
//
// # Flow
//
//  1. Load the existing staff row. A missing row returns
//     ErrStaffNotFound; any other repository failure is wrapped with
//     "get staff for update status: " and returned as a non-sentinel error.
//  2. Write the new status via staffRepo.UpdateStatus.
//  3. Re-fetch via private getByID to obtain the post-update state — which
//     performs its own role lookup.
//  4. Write an audit entry recording the before and after snapshots.
//
// # Re-fetch failure produces a 500 after the write landed
//
// As with Update, step 3 re-reads the row after the status change has
// committed. A failure of the re-fetch (including a failure of the role
// lookup inside it) returns a non-nil error and the handler serves a
// 500, even though the status change has already landed. The audit entry
// is skipped on the re-fetch failure path.
//
// # Absent-field ambiguity is the caller's concern
//
// This method receives `isActive` as a plain bool and writes it directly.
// It does not distinguish "the caller intended to set false" from "the
// caller omitted the field and the zero value defaulted to false" — that
// distinction is not expressible in the current API and is documented on
// UpdateStatusRequest.
//
// # Errors
//
// Returns ErrStaffNotFound only when the target staff row is absent.
// Other initial-lookup failures are wrapped. The status write and
// post-write re-fetch failures are also wrapped rather than collapsed;
// the handler maps them to 500 without disclosing internal details.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent status changes on the same
// account are not serialised; the last write wins.
func (s *StaffService) UpdateStatus(ctx context.Context, id int64, isActive bool) (*Staff, error) {
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
			log.Printf(
				"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
				audit.ACTION_STAFF_UPDATE_STATUS,
				audit.ENTITY_TYPE_STAFF,
				staff.ID,
				staff.Name,
				errLog,
			)
		}
	}

	return staff, err
}

// UpdatePassword hashes and updates the password of a staff user.
// Returns an error if the user is not found, hashing fails, or the
// caller fails the re-authentication or password-policy checks.
// Possible errors: ErrForbidden, ErrInvalidCurrentPassword,
// ErrPasswordTooShort, ErrPasswordTooLong, ErrStaffNotFound.
//
// # Administrative reset with re-authentication
//
// This method replaces the target account's password. It is the
// administrative counterpart to the profile feature's UpdatePassword,
// which requires the caller to supply their own current password as
// proof of account ownership; here the current password proves
// something different. The target's password is not required — the
// caller is resetting someone else's account — but the *actor's* own
// current password is required as a confirmation step (sometimes called
// "sudo mode"). Possession of a valid super-admin token is not on its
// own sufficient to reset another account's password; the caller must
// also demonstrate knowledge of the password that would let them
// authenticate afresh.
//
// # Flow
//
//  1. Call passwordActor. It checks current DB authority, then returns the actor's full
//     staff_users row, including the bcrypt hash of the actor's current
//     password. A missing identity or a non-super-admin role returns
//     authz.ErrUnauthenticated/ErrForbidden before business work.
//  2. Verify the supplied currentPassword against the actor's stored
//     hash via bcrypt.CompareHashAndPassword. A mismatch returns
//     ErrInvalidCurrentPassword.
//  3. Validate newPassword against validatePasswordPolicy. A password
//     outside the min–max length bounds returns the corresponding
//     sentinel.
//  4. Load the target account's row. A missing row returns
//     ErrStaffNotFound; any other repository failure is wrapped with
//     "get staff for update password: " and returned as a non-sentinel
//     error.
//  5. Hash newPassword with bcrypt at bcrypt.DefaultCost. A hashing
//     failure is wrapped with "hash password: " and returned.
//  6. Write the new hash via staffRepo.UpdatePassword.
//  7. Write an audit entry on success. An audit failure is logged and
//     does not abort the operation. The audit entry carries no After
//     payload — see below.
//
// # Ordering of checks
//
// The re-authentication step precedes the password-policy check
// deliberately. A caller who fails re-authentication receives
// ErrInvalidCurrentPassword regardless of the new password's shape,
// which prevents a caller from probing the policy with a valid token
// but an unverified identity. The current-password check also precedes
// the target lookup, so an attacker who has stolen a token but does not
// know the actor's password cannot distinguish "the target account
// exists" from "the target account does not exist".
//
// # Current-password verification
//
// The comparison uses bcrypt.CompareHashAndPassword, which is
// constant-time with respect to the hash and returns a non-nil error on
// any mismatch. A nil actor hash (which would indicate a corrupted row)
// causes the comparison to return an error and the method to return
// ErrInvalidCurrentPassword, which is the safe failure direction.
//
// # Password policy
//
// The new password is validated against validatePasswordPolicy. A
// password shorter than minPasswordLength returns ErrPasswordTooShort;
// a password longer than maxPasswordLength returns ErrPasswordTooLong.
// Both are mapped to 400 Bad Request by the handler.
//
// # bcrypt's 72-byte limit
//
// bcrypt operates on at most 72 bytes of input. The password policy
// rejects inputs above that bound with ErrPasswordTooLong, so the
// library never sees a value it would have to truncate. The wrapped
// "hash password: " error covers the residual case where the library
// rejects an input for a reason other than length.
//
// # Existing sessions survive
//
// A successful password change does not invalidate the target account's
// existing access or refresh tokens. Any active session on the account
// continues to work until its tokens expire; the new password is
// required only for the next login. This is the codebase's no-revocation
// model, and it means an admin-triggered password reset is not on its own
// sufficient to terminate a compromised session.
//
// # Audit entry has no After payload
//
// The audit record written on success carries only the acting staff's
// identity and the target account's ID and name. It does not record the
// password hash — the domain Staff model does not carry it, and
// recording a hash in the audit trail would be a credential-leak risk.
// The audit entry's absence of an After field is deliberate, not an
// oversight; see LogParams in the audit package for the general
// convention.
//
// # Errors
//
// Returns ErrForbidden for insufficient permission and authz.ErrUnauthenticated
// for missing, deleted or inactive identity. Returns
// ErrInvalidCurrentPassword when the supplied confirmation password does
// not match the actor's stored hash. Returns ErrPasswordTooShort or
// ErrPasswordTooLong when the new password violates the policy. Returns
// ErrStaffNotFound only when the target account is absent
// (pgx.ErrNoRows). Other initial-lookup failures are wrapped rather than
// collapsed. bcrypt failures use the prefix "hash password: " and
// repository write failures are returned as-is; both are non-sentinel
// errors served as 500 by the handler. Audit failures are logged and
// discarded.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent UpdatePassword calls on the
// same account will both pass the authorisation, re-authentication, and
// policy checks, both hash their respective new passwords, and the last
// write wins. Two concurrent UpdatePassword calls by the same actor
// succeed independently; the actor's own hash is read at the beginning
// of each call and is not re-read after a change to the actor's own
// account, so a caller cannot invalidate their own in-flight reset by
// resetting their own password in parallel.
func (s *StaffService) UpdatePassword(ctx context.Context, id int64, currentPassword, newPassword string) error {
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
			log.Printf(
				"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
				audit.ACTION_STAFF_UPDATE_PASSWORD,
				audit.ENTITY_TYPE_STAFF,
				existing.ID,
				existing.Name,
				errLog,
			)
		}
	}

	return err
}

// Delete removes a staff user by ID.
// Requires current-DB staff.manage before target reads or other business work.
// Authentication/permission failures use the shared authz errors.
// Prevents self-deletion and logs an audit entry on success.
// Returns ErrStaffNotFound or ErrCannotDeleteSelf.
//
// # Flow
//
//  1. Load the target staff row. A missing row returns
//     ErrStaffNotFound; any other repository failure is wrapped with
//     "get staff for delete: " and returned as a non-sentinel error.
//  2. Compare the authorized principal ID to the delete target.
//     A match returns ErrCannotDeleteSelf.
//  3. Delete the staff row.
//  4. Write an audit entry recording the deletion.
//
// # Hard delete
//
// This is a hard delete of the staff_users row. Any tables that reference
// the row — notably audit_logs, whose actor_user_id may point at the
// deleted account — are subject to whatever foreign-key behaviour the
// schema declares; the service does not cascade or null out references.
// A future report that joins audit_logs to staff_users may therefore
// encounter rows whose actor no longer exists.
//
// # Errors
//
// Returns ErrStaffNotFound only when the target staff row is absent.
// Other initial-lookup failures are wrapped. ErrCannotDeleteSelf is
// returned for a self-delete by the verified principal. Delete
// failures are returned as-is and are served as 500 by the handler.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent Deletes on the same ID will
// both pass the initial lookup and both attempt the delete; the second
// is either a no-op or a row-not-found error at the SQL level depending
// on the delete query's semantics.
func (s *StaffService) Delete(ctx context.Context, id int64) error {
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
			log.Printf(
				"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
				audit.ACTION_STAFF_DELETE,
				audit.ENTITY_TYPE_STAFF,
				existing.ID,
				existing.Name,
				errLog,
			)
		}
	}

	return err
}
