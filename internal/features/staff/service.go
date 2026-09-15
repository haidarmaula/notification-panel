package staff

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"

	"hello/internal/audit"
	"hello/internal/database/sqlc"

	"golang.org/x/crypto/bcrypt"
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
//   - ErrEmailAlreadyUsed is returned by Update, but the handler's Update
//     method does *not* recognise it — see the sentinel's own
//     documentation for the consequence.
//
// The two should probably be a single sentinel; they are currently
// distinct because of the order in which the paths were written. A future
// consolidation would be safe as long as the handler's Update switch is
// extended to recognise the resulting value.
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
	// # Handler mapping gap
	//
	// The handler's Update method does not have a case for this sentinel.
	// It recognises ErrStaffNotFound and ErrInvalidRole explicitly, and
	// every other error — including this one — falls through to the
	// default 500 branch. A caller that attempts to update a staff user's
	// email to a value already taken by another user therefore receives
	// a 500 Internal Server Error with the raw error message, rather
	// than the 409 Conflict that the sentinel's semantics imply.
	//
	// The fix is a one-line addition to the handler's Update switch; it
	// is documented here rather than applied because Rule 3 of this pass
	// prohibits executable changes.
	ErrEmailAlreadyUsed = errors.New("email already used by another staff")

	// ErrCannotDeleteSelf is returned by Delete when the acting staff
	// user — identified from the request context via audit.GetStaffID —
	// is the same as the target of the delete. The guard exists to
	// prevent a super-admin from locking themselves out of the system.
	//
	// # Fail-open on missing context identity
	//
	// The guard is evaluated only when the context carries a staff
	// identity. If audit.GetStaffID returns ok == false, the check is
	// skipped entirely and the delete proceeds. In the current wiring the
	// JWT middleware guarantees a present identity for every authenticated
	// request, so this path is not reachable through the HTTP API. It
	// would be reachable from a caller that invokes Delete with a
	// context that does not carry the middleware's identity value — for
	// example, a background job. See Delete for the full analysis.
	//
	// The handler maps this to 403 Forbidden.
	ErrCannotDeleteSelf = errors.New("cannot delete your own account")
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

// StaffService provides business logic for staff management.
//
// It is the service layer for the administrative staff-user lifecycle:
// provisioning new accounts, reading and listing existing ones, changing
// metadata, toggling active status, resetting passwords, and deleting
// accounts. The service depends on three interfaces declared in
// interfaces.go and is itself abstracted behind StaffServicer for the
// handler's benefit — making this the only feature module in the codebase
// where the handler, the service, and the service's dependencies are all
// interface-typed.
//
// # Dependencies
//
//   - staffRepo — the StaffUserRepository interface, for all staff_users
//     table access.
//   - roleRepo — the RoleRepository interface, for resolving role names
//     to IDs and IDs to names.
//   - auditService — the single-method AuditService interface, for
//     recording administrative actions. Audit failures are logged but do
//     not abort the surrounding operation.
//
// All three must be non-nil. The service does not own them and does not
// dispose of them.
//
// # Authorisation model
//
// The service performs no role-based authorisation of its own. It trusts
// that the caller has already been authorised to perform the operation —
// in the current wiring, the HTTP middleware chain applies the super-admin
// role gate before any staff endpoint is reached. The one exception is
// the self-deletion guard in Delete, which reads the acting staff
// identity from the request context and rejects a delete-of-self. That
// guard is the only place the service inspects the acting identity.
//
// # Concurrency
//
// The service is stateless after construction and safe for concurrent use.
// It holds no per-request state and shares nothing mutable between
// invocations. The three interfaces it holds are expected to be
// concurrency-safe; the concrete implementations backed by pgxpool are.
type StaffService struct {
	staffRepo    StaffUserRepository
	roleRepo     RoleRepository
	auditService AuditService
}

// NewStaffService creates a new StaffService instance.
//
// All three parameters must be non-nil; none is validated, so a nil
// repository or audit service will panic on the first method call that
// uses it rather than at construction. In practice the dependencies are
// wired by NewStaffModule from the shared sqlc.Queries and the shared
// audit service.
//
// # Interface-typed parameters
//
// Because the parameters are declared as interfaces, a caller may pass
// either the concrete repository types from
// internal/database/repository or test fakes. The module passes the
// concrete types; unit tests (see service_test.go in this package) pass
// mockery-generated fakes without a live database.
func NewStaffService(staffRepo StaffUserRepository, roleRepo RoleRepository, auditService AuditService) *StaffService {
	return &StaffService{
		staffRepo:    staffRepo,
		roleRepo:     roleRepo,
		auditService: auditService,
	}
}

// Create creates a new staff user.
// It validates email uniqueness, hashes the password, and assigns a role.
// Returns the created Staff object or an error.
// Possible errors: ErrEmailAlreadyRegistered, ErrInvalidRole.
//
// # Flow
//
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
// # No caller authorisation check
//
// The method does not verify that the acting staff user has authority to
// create another staff user, or to assign any particular role. A caller
// with a valid JWT and the API key can create a super-admin. In the
// current wiring the HTTP layer applies the super-admin gate before this
// method is reached, but the service does not enforce the invariant
// itself.
//
// # Errors
//
// Returns ErrEmailAlreadyRegistered or ErrInvalidRole for the
// corresponding conditions. Returns a wrapped error for the email-check,
// password-hash, or insert steps; none of the wrapped errors is a
// sentinel, so the handler serves them through its default 500 branch.
//
// # Concurrency
//
// Safe for concurrent use. See the advisory-check note above for the
// concurrency implications of the email check.
func (s *StaffService) Create(ctx context.Context, params CreateStaffParams) (*Staff, error) {
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
		After:      staff,
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
//     ErrEmailAlreadyUsed — see the handler-mapping gap noted below.
//  6. Issue the UPDATE.
//  7. Re-fetch via GetByID to obtain the post-update state — a role
//     lookup is performed as part of this re-fetch, which adds a round
//     trip and introduces a failure mode (see below).
//  8. Write an audit entry recording the before and after snapshots. An
//     audit failure is logged and does not abort the operation.
//  9. Return the updated Staff.
//
// # Handler does not map ErrEmailAlreadyUsed to 409
//
// The service returns ErrEmailAlreadyUsed for a duplicate email, but the
// handler's Update switch does not recognise that sentinel — it handles
// only ErrStaffNotFound and ErrInvalidRole, and everything else falls
// through to a 500 with the raw message. A caller attempting to set an
// email already taken by another user therefore receives a 500 rather
// than a 409. This is documented on ErrEmailAlreadyUsed and in the
// handler's Update documentation; correcting it is a handler-side change.
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

	staff, err := s.GetByID(ctx, params.ID)
	if err == nil {
		if errLog := s.auditService.Log(ctx, audit.LogParams{
			Action:     audit.ACTION_STAFF_UPDATE,
			EntityType: audit.ENTITY_TYPE_STAFF,
			EntityName: staff.Name,
			EntityID:   staff.ID,
			Before:     existing,
			After:      staff,
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
// Returns the updated Staff object or an error.
// Possible error: ErrStaffNotFound.
//
// # Flow
//
//  1. Load the existing staff row. A missing row returns
//     ErrStaffNotFound; any other repository failure is wrapped with
//     "get staff for update status: " and returned as a non-sentinel error.
//  2. Write the new status via staffRepo.UpdateStatus.
//  3. Re-fetch via GetByID to obtain the post-update state — which
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

	staff, err := s.GetByID(ctx, id)
	if err == nil {
		if errLog := s.auditService.Log(ctx, audit.LogParams{
			Action:     audit.ACTION_STAFF_UPDATE_STATUS,
			EntityType: audit.ENTITY_TYPE_STAFF,
			EntityName: staff.Name,
			EntityID:   staff.ID,
			Before:     existing,
			After:      staff,
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
// Returns an error if the user is not found or hashing fails.
// Possible error: ErrStaffNotFound.
//
// # Administrative reset, not a self-service change
//
// This method replaces the target account's password without requiring
// the caller to supply the current password. It is the administrative
// counterpart to the profile feature's UpdatePassword, which does require
// the current password; the two features have distinct authorisation
// models (super-admin acting on another account, versus a staff user
// acting on their own).
//
// Because the current password is not verified, this method relies
// entirely on the HTTP-layer super-admin gate to prevent unauthorised
// use. The service does not independently check the acting user's
// authority, nor does it prevent a caller from resetting a
// higher-privileged account's password.
//
// # No password policy
//
// The method performs no validation of the new password's length,
// character composition, or similarity to the previous password. Any
// string that bcrypt will accept is stored. A caller that requires
// minimum length, complexity rules, or a "must differ" check must
// implement it at the handler layer or in a dedicated helper.
//
// # bcrypt's 72-byte limit
//
// bcrypt operates on at most 72 bytes of input; a longer password is
// either rejected by the library (returning ErrPasswordTooLong) or
// silently truncated, depending on the library version. Either way the
// method propagates the behaviour; the wrapped "hash password: " error
// covers the rejection case.
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
// Returns ErrStaffNotFound only when the target account is absent
// (pgx.ErrNoRows). Other initial-lookup failures are wrapped rather than
// collapsed. bcrypt failures use the prefix "hash password: " and
// repository write failures are returned as-is; both are non-sentinel
// errors served as 500 by the handler. Audit failures are logged and
// discarded.
//
// # Concurrency
//
// Not serialised against concurrent resets. Two concurrent UpdatePassword
// calls on the same account will both hash their respective new passwords
// and the last write wins.
func (s *StaffService) UpdatePassword(ctx context.Context, id int64, newPassword string) error {
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
// Prevents self-deletion and logs an audit entry on success.
// Returns ErrStaffNotFound or ErrCannotDeleteSelf.
//
// # Flow
//
//  1. Load the target staff row. A missing row returns
//     ErrStaffNotFound; any other repository failure is wrapped with
//     "get staff for delete: " and returned as a non-sentinel error.
//  2. Read the acting staff identity from the request context via
//     audit.GetStaffID and, if present, compare it to the delete target.
//     A match returns ErrCannotDeleteSelf.
//  3. Delete the staff row.
//  4. Write an audit entry recording the deletion.
//
// # The self-deletion guard is fail-open on missing context identity
//
// The guard is evaluated only when the context carries a staff identity:
//
//	actorID, ok := audit.GetStaffID(ctx)
//	if ok && actorID == id {
//	    return ErrCannotDeleteSelf
//	}
//
// If audit.GetStaffID returns ok == false — the context has no staff
// identity, or the identity is of the wrong type — the check is skipped
// and the delete proceeds. In the current wiring every authenticated HTTP
// request carries a staff identity set by the JWT middleware, so this
// path is not reachable through the API. It would be reachable from a
// caller that invokes Delete with a plain context.Background(), such as
// a background job or a test that does not populate the context. The
// safety property ("a super-admin cannot delete themselves") is
// therefore enforced conditionally, not absolutely.
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
// returned for a self-delete with a present context identity. Delete
// failures are returned as-is and are served as 500 by the handler.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent Deletes on the same ID will
// both pass the initial lookup and both attempt the delete; the second
// is either a no-op or a row-not-found error at the SQL level depending
// on the delete query's semantics.
func (s *StaffService) Delete(ctx context.Context, id int64) error {
	existing, err := s.staffRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStaffNotFound
		}
		return fmt.Errorf("get staff for delete: %w", err)
	}

	actorID, ok := audit.GetStaffID(ctx)
	if ok && actorID == id {
		return ErrCannotDeleteSelf
	}

	err = s.staffRepo.Delete(ctx, id)
	if err == nil {
		if errLog := s.auditService.Log(ctx, audit.LogParams{
			Action:     audit.ACTION_STAFF_DELETE,
			EntityType: audit.ENTITY_TYPE_STAFF,
			EntityName: existing.Name,
			EntityID:   existing.ID,
			Before:     existing,
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
