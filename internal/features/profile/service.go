package profile

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
)

// Domain errors.
//
// These sentinels are returned by ProfileService methods and translated into
// HTTP status codes by ProfileHandler. Unlike the notification and mobile
// features, this package does not have an errors.go — the three sentinels
// are declared here, next to the methods that return them.
var (
	// ErrProfileNotFound is returned when the staff row identified by a
	// staff ID cannot be loaded. Note that it is returned for *any*
	// repository error from the initial FindByID call, not only for
	// pgx.ErrNoRows: a database connectivity failure or a query error
	// produces the same sentinel, and the client cannot distinguish the
	// two. This matches the pattern used by auth.Login and by
	// notifications.GetByID, but it means a transient infrastructure
	// problem is reported to the client as "profile not found".
	//
	// The handler maps this to 404 Not Found.
	ErrProfileNotFound = errors.New("profile not found")

	// ErrInvalidCredentials is returned by UpdatePassword when the supplied
	// current_password does not match the bcrypt hash stored on the staff
	// row. It is intentionally named and messaged to be indistinguishable
	// from a wrong-password failure at the login endpoint — the message
	// ("current password is incorrect") tells the caller the specific field
	// is wrong, but the sentinel exists so that a caller cannot learn from
	// the error type alone whether the staff row exists.
	//
	// The handler maps this to 401 Unauthorized.
	ErrInvalidCredentials = errors.New("current password is incorrect")

	// ErrEmailAlreadyUsed is returned by UpdateProfile when the new email
	// is already registered to a different staff account. The check that
	// produces it — staffRepo.ExistsByEmail — is not atomic with the
	// subsequent update, so a concurrent request claiming the same email
	// can race past this check; see UpdateProfile for the details.
	//
	// The handler maps this to 409 Conflict.
	ErrEmailAlreadyUsed = errors.New("email already used by another account")
)

// UpdateProfileParams holds input for updating profile.
//
// It is the service-layer counterpart of UpdateProfileRequest. Because both
// fields are plain strings, the struct cannot represent "field omitted"; the
// UpdateProfile method uses the empty string as the sentinel for "leave the
// existing value unchanged". A caller that genuinely wants to set the name
// or email to an empty string cannot do so through this API — the request
// would be interpreted as a no-op for that field.
//
// UpdateProfileParams is a plain data struct, constructed on the stack by
// the handler and passed by value.
type UpdateProfileParams struct {
	// ID is the internal staff_users.id of the account being updated. In
	// the current handler wiring it always equals the acting staff user's
	// ID, taken from the JWT claims; the service does not independently
	// verify that the caller has authority to edit the identified row.
	ID int64

	// Name is the new display name. An empty string means "do not change
	// the name". A non-empty value that matches the current name is
	// treated as a no-op by the same branch that would otherwise apply it.
	Name string

	// Email is the new login email. An empty string means "do not change
	// the email". A non-empty value that matches the current email is
	// skipped without an existence check, so a client that resends the
	// current email does not trigger a spurious ErrEmailAlreadyUsed.
	Email string
}

// ProfileService provides business logic for staff profile management.
//
// It is the business-logic layer of the profile feature: it reads a staff
// row and its associated role, applies updates with bcrypt for password
// changes, and returns the domain Profile. It performs no HTTP handling and
// no JWT verification — the handler layer has already established the
// acting staff user's identity from the token before invoking any of these
// methods.
//
// # Dependencies
//
// The service holds two repositories:
//
//   - staffRepo, used to read and update the staff_users row that is the
//     target of every operation.
//   - roleRepo, used to resolve the role name for the profile view.
//
// Both are stored by reference and must be non-nil. The service does not
// own them and does not close or dispose of them.
//
// # Authorisation model
//
// The service does not perform any authorisation checks of its own. Its
// methods receive a target staff ID from the caller and act on it without
// questioning whether the caller is permitted to do so. The profile feature
// relies on the handler layer always passing the acting user's own ID —
// which it does, because the ID comes from the JWT claims and there is no
// path parameter that would allow a caller to name a different account.
//
// If a future revision adds a route that accepts a staff ID from the URL
// and passes it to these methods — for example, an administrative "edit
// any staff profile" endpoint — the service's methods will happily
// operate on the named account. The authorisation check would need to be
// added at that layer, not here.
//
// # Concurrency
//
// The service is stateless after construction and safe for concurrent use.
// It holds no per-request state and shares nothing mutable between
// invocations. The two repositories it holds are themselves safe for
// concurrent use; concurrency issues that arise do so at the SQL level
// (see UpdateProfile's TOCTOU note) rather than in this type.
type ProfileService struct {
	staffRepo *repository.StaffUserRepository
	roleRepo  *repository.RoleRepository
}

// NewProfileService creates a new ProfileService instance.
//
// Both parameters must be non-nil; neither is validated, so a nil
// repository will panic on the first method call that uses it rather than
// at construction. In practice both are wired by NewProfileModule from the
// shared sqlc.Queries.
//
// The returned service is cheap to construct and holds no external
// resources. There is no Close method — none is needed.
func NewProfileService(staffRepo *repository.StaffUserRepository, roleRepo *repository.RoleRepository) *ProfileService {
	return &ProfileService{
		staffRepo: staffRepo,
		roleRepo:  roleRepo,
	}
}

// GetProfile retrieves the profile of the authenticated staff user.
//
// # Flow
//
// The method loads the staff row by ID via staffRepo.FindByID, then loads
// the associated role via roleRepo.FindByID to obtain the role name. It
// constructs a Profile from the two rows and returns it. No authorisation
// check is performed — see the type-level documentation for the
// authorisation model.
//
// # Error handling on the staff lookup
//
// A missing profile row returns ErrProfileNotFound only when the underlying
// repository error is pgx.ErrNoRows. Any other lookup failure — a database
// connectivity error, a query failure, or another driver-level problem — is
// wrapped with "get profile: " and returned as a non-sentinel error, so the
// handler serves it as 500 rather than incorrectly reporting 404.
//
// # Error wrapping on the role lookup
//
// A failure of the role lookup is wrapped with the prefix "get role: " and
// returned as a non-sentinel error. The handler's switch does not
// recognise this wrapping, so the error surfaces through the default 500
// branch and its message is served to the client verbatim. The message is
// the underlying repository error, which may include driver-level detail.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent calls may observe different
// states of the staff row if an update is in flight between their two
// reads.
func (s *ProfileService) GetProfile(ctx context.Context, staffID int64) (*Profile, error) {
	staff, err := s.staffRepo.FindByID(ctx, staffID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProfileNotFound
		}
		return nil, fmt.Errorf("get profile: %w", err)
	}

	role, err := s.roleRepo.FindByID(ctx, staff.RoleID)
	if err != nil {
		return nil, fmt.Errorf("get role: %w", err)
	}

	return &Profile{
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

// UpdateProfile updates the name and/or email of the authenticated staff user.
//
// # Flow
//
//  1. Load the existing staff row by params.ID. A missing row returns
//     ErrProfileNotFound; any other repository failure is wrapped and
//     returned as a non-sentinel error.
//  2. Build an update struct initialised with the existing values, so that
//     fields not supplied by the caller retain their current state.
//  3. If params.Name is non-empty, set it on the update struct. An empty
//     params.Name leaves the existing name in place.
//  4. If params.Email is non-empty AND differs from the existing email,
//     check for a collision via ExistsByEmail. A collision produces
//     ErrEmailAlreadyUsed. An empty params.Email, or one equal to the
//     current email, is skipped without a check — a client resending the
//     current email does not trigger a spurious conflict.
//  5. If neither field changed, return the current profile without
//     writing.
//  6. Otherwise, apply the update and re-fetch the profile via GetProfile.
//
// # TOCTOU between existence check and update
//
// The email-collision check at step 4 and the actual write at step 6 are
// two separate database round-trips with no locking between them. Two
// concurrent UpdateProfile calls that both target the same new email will
// both see ExistsByEmail == false and both proceed to the write. The
// outcome depends entirely on whether the staff_users table declares a
// UNIQUE constraint on email:
//
//   - If the schema enforces uniqueness, the second write fails at the
//     database with a unique-violation error, which the service wraps with
//     the prefix "update profile: " and the handler serves as a 500. The
//     client sees a generic 500 rather than the 409 Conflict that
//     ErrEmailAlreadyUsed would have produced.
//   - If the schema does not enforce uniqueness, both writes succeed and
//     the table ends up with two staff users sharing an email address.
//
// The existence check is therefore an advisory optimisation, not a
// correctness guarantee. The authoritative protection is the database
// constraint; the sentinel is a fast path for the common case.
//
// # Stale-read on re-fetch
//
// The successful path returns the result of a second GetProfile call,
// which reads the row again. Between the update and the re-fetch, a
// concurrent update from another request can change the row, so the
// returned profile is not necessarily the state this call wrote. The
// window is small and the consequence benign (the caller sees a slightly
// newer state than their own write), but the method does not return the
// row it just wrote.
//
// # Errors
//
// Returns ErrProfileNotFound only when the initial lookup returns
// pgx.ErrNoRows. Other lookup failures are wrapped with
// "get profile for update: ". Returns ErrEmailAlreadyUsed if the email
// collision check fails, a wrapped error with the prefix "check email: " if
// the collision check itself errors, and a wrapped error with the prefix
// "update profile: " if the write fails. Wrapped errors are not sentinels,
// so the handler serves them through the default 500 branch.
//
// # Authorisation
//
// No check is performed that the caller is permitted to edit the row
// identified by params.ID. The current handler always passes the acting
// user's own ID, but the service does not enforce this — a future caller
// that passes a different ID would edit that account.
//
// # Concurrency
//
// Not serialised against concurrent updates to the same staff row. The
// last write wins; the intermediate states are visible to concurrent
// readers.
func (s *ProfileService) UpdateProfile(ctx context.Context, params UpdateProfileParams) (*Profile, error) {
	existing, err := s.staffRepo.FindByID(ctx, params.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProfileNotFound
		}
		return nil, fmt.Errorf("get profile for update: %w", err)
	}

	update := sqlc.UpdateStaffUserParams{
		ID:     params.ID,
		RoleID: existing.RoleID,
		Name:   existing.Name,
		Email:  existing.Email,
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

	// Only perform update if there are changes
	if update.Name == existing.Name && update.Email == existing.Email {
		return s.GetProfile(ctx, params.ID)
	}

	if err := s.staffRepo.Update(ctx, update); err != nil {
		return nil, fmt.Errorf("update profile: %w", err)
	}

	return s.GetProfile(ctx, params.ID)
}

// UpdatePassword changes the password for the authenticated staff user.
//
// # Flow
//
//  1. Load the staff row by ID. A failure returns ErrProfileNotFound.
//  2. Compare the supplied currentPassword against the stored bcrypt hash
//     via bcrypt.CompareHashAndPassword. A mismatch returns
//     ErrInvalidCredentials.
//  3. Hash the supplied newPassword with bcrypt at bcrypt.DefaultCost.
//     A hashing failure is wrapped with "hash password: " and returned
//     as a non-sentinel error.
//  4. Write the new hash via staffRepo.UpdatePassword. A write failure is
//     wrapped with "update password: " and returned as a non-sentinel
//     error.
//
// # No password policy is applied
//
// The method performs no validation of the new password's length,
// character composition, or similarity to the current password. Any string
// that bcrypt will accept is stored. A caller that requires minimum
// length, complexity rules, or a "must differ from current" check must
// implement it at the handler layer or in a dedicated password-policy
// helper; this method provides none of those.
//
// # bcrypt's 72-byte limit
//
// bcrypt operates on at most 72 bytes of input. A newPassword longer than
// 72 bytes will either be rejected by the underlying library (recent
// versions of golang.org/x/crypto/bcrypt return ErrPasswordTooLong) or
// silently truncated to its first 72 bytes (older versions). Either way,
// the method propagates whatever the library does; a caller supplying a
// very long password should not assume the full value is stored. The
// generation step's wrapped error path covers the rejection case.
//
// # Password rotation does not invalidate tokens
//
// A successful password change does not invalidate any existing access or
// refresh tokens for the account. The caller's current session continues
// to work, and so do any other sessions on other devices. The new password
// is required only for the next login. This is the same no-revocation model
// documented in the token package.
//
// # Timing on the comparison
//
// bcrypt.CompareHashAndPassword is a constant-time comparison relative to
// the hash and password lengths. The method does not short-circuit before
// the comparison based on any property of the supplied currentPassword,
// so an attacker cannot learn anything about the stored hash from the
// response time of this endpoint beyond what the bcrypt comparison itself
// already constrains.
//
// # Errors
//
// Returns ErrProfileNotFound, ErrInvalidCredentials, or a wrapped error
// for the hash-generation and write steps. The wrapped errors are not
// sentinels and will be served through the handler's default 500 branch.
//
// # Concurrency
//
// Not serialised against concurrent password changes for the same account.
// Two concurrent changes with the same currentPassword will both pass
// verification, both hash their respective new passwords, and the last
// write wins; see the handler's documentation for the practical
// consequence.
func (s *ProfileService) UpdatePassword(ctx context.Context, staffID int64, currentPassword, newPassword string) error {
	staff, err := s.staffRepo.FindByID(ctx, staffID)
	if err != nil {
		return ErrProfileNotFound
	}

	// Verify current password
	if err := bcrypt.CompareHashAndPassword([]byte(staff.PasswordHash), []byte(currentPassword)); err != nil {
		return ErrInvalidCredentials
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	if err := s.staffRepo.UpdatePassword(ctx, sqlc.UpdateStaffPasswordParams{
		ID:           staffID,
		PasswordHash: string(hashed),
	}); err != nil {
		return fmt.Errorf("update password: %w", err)
	}

	return nil
}
