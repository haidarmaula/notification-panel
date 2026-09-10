package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

// UserRepository provides read and write access to the users table.
//
// Users in this system are end users of the mobile application — distinct
// from staff users, who operate the admin portal. They are the audience of
// every notification, the owners of every device token, and the members of
// every segment. The repository is the read-heavy counterpart to
// StaffUserRepository and is consumed by the users feature service, the
// mobile integration service, and the worker's audience-resolution stage.
//
// # Identity model
//
// A user row carries three distinct identifiers, each with a different
// provenance:
//
//   - The internal surrogate primary key (id), used by every foreign-key
//     reference in the system — device_tokens, segment_members,
//     notification_deliveries.
//   - An external identifier (external_id), issued by an upstream system —
//     most likely the Laravel mobile backend — and stable across this
//     service's own migrations. It is the natural key for mobile-facing
//     lookups and for idempotent ingestion.
//   - An email address, which is nullable at the schema level (hence
//     pgtype.Text) because not every mobile user completes registration.
//
// Callers should prefer the identifier that matches the caller's own
// provenance: external_id from the mobile backend, id from internal
// foreign keys, and email only for explicit user-initiated searches.
//
// # Nullable email
//
// FindByEmail and ExistsByEmail accept pgtype.Text rather than string
// because the underlying column is nullable. This is a deliberate
// consequence of the schema, but it places a small burden on callers: they
// must construct a pgtype.Text value themselves. Callers who want to look up
// a non-null email should construct it with Valid: true; passing a
// pgtype.Text with Valid: false has the semantics of "match rows where email
// IS NULL", which is rarely the intent.
type UserRepository struct {
	q sqlc.Querier
}

// NewUserRepository constructs a UserRepository bound to q.
//
// q must be non-nil. Typical callers are the users feature service, the
// mobile integration service, and the notification worker's audience
// resolution stage. As with the other repositories in this package, a
// transaction-scoped querier or a test fake is equally valid.
func NewUserRepository(q sqlc.Querier) *UserRepository {
	return &UserRepository{q: q}
}

// Count returns the total number of user rows, unfiltered.
//
// It is intended for admin dashboard totals and pagination metadata, not for
// per-request filtering. The count includes users in every status — active,
// inactive, or otherwise — unless the underlying query adds a predicate.
// Use CountByStatus when the caller needs to display a status-scoped total.
func (r *UserRepository) Count(ctx context.Context) (int64, error) {
	return r.q.CountUsers(ctx)
}

// CountByStatus returns the number of users currently in the given status.
//
// It is the status-scoped counterpart of Count and is typically used to
// populate the summary cards on the admin user list ("12,341 active · 208
// suspended"). The status value is passed through to SQL verbatim — the
// repository performs no validation against a fixed set of allowed values,
// so a typo will simply return 0 rather than an error.
func (r *UserRepository) CountByStatus(ctx context.Context, status string) (int64, error) {
	return r.q.CountUsersByStatus(ctx, status)
}

// FindByID retrieves a user by its internal primary key and returns the base
// sqlc.User model.
//
// This is the natural lookup from any internal context that already holds a
// user ID — for example, when resolving a segment member's details, or when
// the worker is annotating a delivery record. pgx.ErrNoRows is returned
// unchanged when no row matches; callers translate it to a 404 at the HTTP
// layer.
func (r *UserRepository) FindByID(ctx context.Context, id int64) (sqlc.User, error) {
	return r.q.GetUserByID(ctx, id)
}

// FindByExternalID retrieves a user by the identifier issued by the upstream
// system (typically the Laravel mobile backend).
//
// This is the natural lookup from mobile-facing code paths, where the
// external identifier is what the client knows and what survives a
// re-ingestion of user data. It is also the identity key used by ingestion
// to distinguish new users from updates to existing ones. pgx.ErrNoRows is
// returned unchanged when no row matches.
func (r *UserRepository) FindByExternalID(ctx context.Context, externalID string) (sqlc.User, error) {
	return r.q.GetUserByExternalID(ctx, externalID)
}

// FindByEmail retrieves a user by email address.
//
// The email column is nullable, which is why this method accepts pgtype.Text
// rather than a plain string. Callers looking up a specific email should
// construct the argument as pgtype.Text{String: email, Valid: true}; a
// pgtype.Text with Valid: false would match rows with a NULL email, which is
// unlikely to be the caller's intent.
//
// Email is not a stable identifier in this system — mobile users may change
// it, and it is optional at registration — so prefer FindByID or
// FindByExternalID when the caller has a choice. pgx.ErrNoRows is returned
// unchanged when no row matches.
func (r *UserRepository) FindByEmail(ctx context.Context, email pgtype.Text) (sqlc.User, error) {
	return r.q.GetUserByEmail(ctx, email)
}

// List returns a page of users ordered by the query's default sort
// (typically newest first), without applying any filters.
//
// offset and limit are passed through to SQL without validation, following
// the package convention. For status-scoped lists, use ListByStatus; for
// keyword searches, use Search.
func (r *UserRepository) List(ctx context.Context, offset, limit int32) ([]sqlc.User, error) {
	return r.q.ListUsers(ctx, sqlc.ListUsersParams{
		Offset: offset,
		Limit:  limit,
	})
}

// ListByStatus returns a page of users currently in the given status.
//
// It pairs naturally with CountByStatus so that the admin portal's filtered
// list view can render accurate pagination metadata. The status value is
// passed through to SQL verbatim; the repository performs no validation
// against a fixed set of allowed values. offset and limit are passed through
// without validation.
func (r *UserRepository) ListByStatus(ctx context.Context, status string, offset, limit int32) ([]sqlc.User, error) {
	return r.q.ListUsersByStatus(ctx, sqlc.ListUsersByStatusParams{
		Status: status,
		Offset: offset,
		Limit:  limit,
	})
}

// Search returns a page of users whose identity fields match keyword.
//
// As with the other Search methods in this package, keyword is wrapped in
// pgtype.Text{Valid: true} unconditionally: an empty keyword is treated as a
// valid search term rather than as "no filter". Callers that want
// "keyword is optional" semantics should use List or ListByStatus instead.
//
// The query is expected to match against name, email, or external_id —
// consult db/queries/users.sql for the exact predicate — but the repository
// makes no promise about which columns participate.
func (r *UserRepository) Search(ctx context.Context, keyword string, offset, limit int32) ([]sqlc.User, error) {
	return r.q.SearchUsers(ctx, sqlc.SearchUsersParams{
		Keyword: pgtype.Text{String: keyword, Valid: true},
		Offset:  offset,
		Limit:   limit,
	})
}

// Create inserts a new user and returns the persisted row, including any
// database-generated fields such as the internal primary key and creation
// timestamp.
//
// The caller is responsible for supplying a unique external_id (and, if
// present, a unique email): the repository does not check for collisions
// before inserting. A duplicate will surface as a pgx unique-violation
// error, which ingestion code should handle by falling back to an update
// path — typically via FindByExternalID followed by Update. For idempotent
// ingestion, callers that want to avoid the constraint-violation round-trip
// should consult ExistsByExternalID first.
func (r *UserRepository) Create(ctx context.Context, params sqlc.CreateUserParams) (sqlc.User, error) {
	return r.q.CreateUser(ctx, params)
}

// Update applies a metadata modification to a user.
//
// Which columns can be changed is defined by sqlc.UpdateUser. The repository
// performs no validation of the new values and no conflict checking against
// other rows — the caller is responsible for ensuring any uniqueness
// constraint (for example, on email) is respected, or for handling the
// unique-violation error that will surface if it is not. Status changes are
// deliberately not part of this method; use UpdateStatus for those so that
// the intent is visible at call sites and so that any per-transition side
// effects can be attached to the SQL.
func (r *UserRepository) Update(ctx context.Context, params sqlc.UpdateUserParams) error {
	return r.q.UpdateUser(ctx, params)
}

// UpdateStatus changes the active/suspended status of a user.
//
// This is the mechanism used to suspend an account without deleting it,
// preserving audit history, delivery records, and segment membership. The
// repository performs an unconditional UPDATE — it does not read the current
// status first — so applying a transition to a row already in the target
// state is a no-op at the SQL level. Suspending a user does not deactivate
// their device tokens; a caller that requires that invariant must also call
// DeviceTokenRepository.UpdateStatus for each token.
func (r *UserRepository) UpdateStatus(ctx context.Context, params sqlc.UpdateUserStatusParams) error {
	return r.q.UpdateUserStatus(ctx, params)
}

// Delete removes a user by primary key.
//
// This is a hard delete and is not the recommended way to retire an account
// whose history matters — prefer UpdateStatus to suspend. The operation is
// subject to whatever foreign-key behaviour the schema declares on tables
// that reference users (notably device_tokens, segment_members, and
// notification_deliveries); the repository does not cascade or null out
// references itself, so a user that still appears in those tables will
// typically fail with a foreign-key violation. Callers that need to delete a
// user and their dependents atomically must orchestrate that themselves
// inside a shared transaction. Deleting a non-existent row is not an error.
func (r *UserRepository) Delete(ctx context.Context, id int64) error {
	return r.q.DeleteUser(ctx, id)
}

// ExistsByExternalID reports whether a user with the given external
// identifier already exists.
//
// It is the cheap pre-check for Create in ingestion pipelines: a caller that
// wants to avoid triggering a unique-constraint violation can call this
// first and route to an Update path if the row already exists. The method
// performs a single indexed lookup and returns false, nil when no row is
// found — pgx.ErrNoRows is swallowed by the query rather than surfaced.
func (r *UserRepository) ExistsByExternalID(ctx context.Context, externalID string) (bool, error) {
	return r.q.ExistsUserByExternalID(ctx, externalID)
}

// ExistsByEmail reports whether a user with the given email already exists.
//
// As with FindByEmail, the email column is nullable, which is why this
// method accepts pgtype.Text rather than a plain string. Callers checking
// for a specific email should construct the argument as
// pgtype.Text{String: email, Valid: true}; a pgtype.Text with Valid: false
// would test for rows with a NULL email, which is rarely the intent.
//
// The method is the cheap pre-check for Create and for email-changing
// Update calls. It returns false, nil when no row is found — pgx.ErrNoRows
// is swallowed by the query rather than surfaced.
func (r *UserRepository) ExistsByEmail(ctx context.Context, email pgtype.Text) (bool, error) {
	return r.q.ExistsUserByEmail(ctx, email)
}
