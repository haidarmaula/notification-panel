package repository

import (
	"context"

	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

// StaffUserRepository provides database access for staff users.
//
// Staff users are the human operators of the admin portal — distinct from
// end users, who are represented by UserRepository. A staff user belongs to
// exactly one role, holds a bcrypt password hash, and carries an active
// status flag. Authentication, password changes, and status toggles are all
// modelled here as discrete methods so call sites are self-documenting and
// so that per-operation side effects (such as bumping a credential
// timestamp) can be attached to the SQL rather than duplicated in Go.
//
// The repository does not hash passwords: callers must supply an
// already-bcrypt-hashed value in the params to Create and UpdatePassword.
// This keeps the repository free of cryptographic policy, at the cost of
// requiring every caller to be aware of the contract.
type StaffUserRepository struct {
	q sqlc.Querier
}

// GetStaffAuthority reads only identity, active status and role in one snapshot.
// A transaction-bound querier can be used for future guarded mutations.
func (r *StaffUserRepository) GetStaffAuthority(ctx context.Context, id int64) (sqlc.GetStaffAuthorityRow, error) {
	return r.q.GetStaffAuthority(ctx, id)
}

// NewStaffUserRepository creates a new StaffUserRepository instance.
//
// q must be non-nil. Typical callers are the auth service (login and
// refresh flows), the staff admin service (CRUD), the JWT middleware (the
// per-request active-status check), and the bootstrap utility (initial
// super-admin provisioning). A transaction-scoped querier or a test fake is
// equally valid.
func NewStaffUserRepository(q sqlc.Querier) *StaffUserRepository {
	return &StaffUserRepository{q: q}
}

// Count returns the total number of staff users, unfiltered.
//
// It is intended for admin dashboard totals and pagination metadata, not for
// per-request filtering. The query counts all rows regardless of status, so
// deactivated staff still contribute to the total unless the SQL adds a
// predicate.
func (r *StaffUserRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountStaffUsers(ctx)
}

// FindByID retrieves a staff user by primary key and returns the base
// sqlc.StaffUser model.
//
// The model carries the password hash — callers that render or log the
// result must ensure the hash is not exposed. pgx.ErrNoRows is returned
// unchanged when no row matches.
func (r *StaffUserRepository) FindByID(ctx context.Context, id int64) (sqlc.StaffUser, error) {
	return r.q.GetStaffUserByID(ctx, id)
}

// SessionActive checks the signed family and the account's current authentication generation.
func (r *StaffUserRepository) SessionActive(ctx context.Context, staffID int64, sessionID string) (bool, error) {
	var id pgtype.UUID
	if err := id.Scan(sessionID); err != nil {
		return false, nil
	}
	return r.q.StaffSessionActive(ctx, sqlc.StaffSessionActiveParams{StaffID: staffID, SessionID: id})
}

// FindByEmail retrieves a staff user by email address and returns the base
// sqlc.StaffUser model.
//
// It is the primary lookup during login and during the bootstrap utility's
// idempotency check. Email is expected to be unique in the schema; the
// query returns pgx.ErrNoRows when no row matches, and the bootstrap
// utility in particular relies on exactly that error to distinguish
// "already provisioned" from "real failure".
func (r *StaffUserRepository) FindByEmail(ctx context.Context, email string) (sqlc.StaffUser, error) {
	return r.q.GetStaffUserByEmail(ctx, email)
}

// List returns a paginated list of staff users with role names.
//
// The returned row type is sqlc.ListStaffUsersRow, which joins the role
// table so the admin list view can display the human-readable role label
// without a second query. offset and limit are passed through to SQL
// without validation, following the package convention.
func (r *StaffUserRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.ListStaffUsersRow, error) {
	return r.q.ListStaffUsers(ctx, sqlc.ListStaffUsersParams{
		Offset: offset,
		Limit:  limit,
	})
}

// Search returns a paginated list of staff users matching a keyword (by name or email).
//
// As with the other Search methods in this package, keyword is wrapped in
// pgtype.Text{Valid: true} unconditionally: an empty keyword is treated as a
// valid search term rather than as "no filter". Callers that want "keyword
// is optional" semantics should use List. The returned row type carries the
// same joined role context as List.
func (r *StaffUserRepository) Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.SearchStaffUsersRow, error) {
	return r.q.SearchStaffUsers(ctx, sqlc.SearchStaffUsersParams{
		Keyword: pgtype.Text{String: keyword, Valid: true},
		Offset:  offset,
		Limit:   limit,
	})
}

// Create inserts a new staff user record.
//
// The caller must supply an already-bcrypt-hashed password in
// params.PasswordHash; this method performs no hashing. Email is expected to
// be unique in the schema, and a duplicate will surface as a pgx error. The
// returned model carries database-generated fields such as the primary key
// and creation timestamp.
func (r *StaffUserRepository) Create(ctx context.Context, params sqlc.CreateStaffUserParams) (sqlc.StaffUser, error) {
	return r.q.CreateStaffUser(ctx, params)
}

// Update modifies an existing staff user's role, name, or email.
//
// Password and status changes are deliberately not part of this method —
// they are exposed as separate operations (UpdatePassword and UpdateStatus)
// so that each can carry its own side effects (for example, bumping a
// credential timestamp on password change) and so that audit entries can
// distinguish profile edits from security-relevant changes. As with Create,
// email changes are subject to the unique constraint and may fail with a
// pgx error.
func (r *StaffUserRepository) Update(ctx context.Context, params sqlc.UpdateStaffUserParams) error {
	return r.q.UpdateStaffUser(ctx, params)
}

// UpdatePassword changes the password hash of a staff user.
//
// The caller must supply an already-bcrypt-hashed value in
// params.PasswordHash; this method performs no hashing. The plaintext
// password never reaches the repository. The operation does not revoke any
// outstanding refresh tokens — a caller that requires that invariant must
// handle it at the service layer or via a token-version column if one is
// added later.
func (r *StaffUserRepository) UpdatePassword(ctx context.Context, params sqlc.UpdateStaffPasswordParams) error {
	return r.q.UpdateStaffPassword(ctx, params)
}

// UpdateStatus changes the active status of a staff user.
//
// This is the mechanism used to deactivate an account without deleting it,
// preserving audit history and any foreign-key references from other tables.
// The repository performs an unconditional UPDATE — it does not read the
// current status first — so applying a transition to a row that is already
// in the target state is a no-op at the SQL level.
//
// # Interaction with IsActive
//
// A transition to false takes effect for the JWT middleware on the next
// authenticated request: the middleware re-reads the status on every
// request, so an already-issued access token stops working as soon as this
// method commits. No cache or revocation list is involved.
func (r *StaffUserRepository) UpdateStatus(ctx context.Context, params sqlc.UpdateStaffStatusParams) error {
	return r.q.UpdateStaffStatus(ctx, params)
}

// Delete removes a staff user by ID.
//
// This is a hard delete and is not the recommended way to retire an account
// whose history matters — prefer UpdateStatus to deactivate. Use Delete only
// when the caller has determined the row should no longer exist, such as
// during cleanup of a mistakenly-created account. The operation is subject
// to whatever foreign-key behaviour the schema declares on tables that
// reference staff_users (notably audit_logs); the repository does not
// cascade or null out references itself. Deleting a non-existent row is not
// an error.
//
// # Interaction with IsActive
//
// A deleted row is reported by IsActive as (false, nil), so an outstanding
// access token for the deleted account stops working on the next
// authenticated request in the same way a deactivated account does.
func (r *StaffUserRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteStaffUser(ctx, id)
}

// ExistsByEmail checks whether a staff user with the given email already exists.
//
// It is the cheap pre-check for Create and for email-changing Update calls:
// a caller that wants to avoid triggering a unique-constraint violation can
// call this first. The method performs a single indexed lookup on the email
// and returns false, nil when no row is found — pgx.ErrNoRows is swallowed
// by the query rather than surfaced.
func (r *StaffUserRepository) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	return r.q.ExistsStaffUserByEmail(ctx, email)
}

// CountMatching counts the same name/email search as Search.
func (r *StaffUserRepository) CountMatching(ctx context.Context, search string) (int64, error) {
	if search == "" {
		return r.Count(ctx)
	}
	return r.q.CountMatchingStaff(ctx, search)
}
