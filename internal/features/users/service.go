package users

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
)

// Domain errors.
//
// These five sentinels are returned by UserService methods and translated
// into HTTP status codes by the UserHandler and DeviceTokenHandler. They
// are declared here, in the file that owns their methods, rather than in a
// separate errors.go — consistent with the convention used by the auth,
// notifications, segments, and staff features.
//
// # Error collapse policy
//
// Every method in this file that performs a by-ID or by-external-ID lookup
// distinguishes pgx.ErrNoRows — the driver's signal that the row does not
// exist — from every other repository failure. ErrUserNotFound and
// ErrDeviceTokenNotFound are therefore returned only for genuinely absent
// rows; a connectivity failure, a query error, or any other driver-level
// problem is wrapped with a method-specific prefix and propagated as a
// non-sentinel error, which the handler maps to 500 rather than 404. This
// closes the class of defect described as C16 in code-review.md, where a
// database failure was indistinguishable from "no such record".
var (
	// ErrUserNotFound is returned when a user ID or external_id does not
	// resolve to a row. Every by-ID and by-external-ID lookup in this
	// package returns it only for pgx.ErrNoRows; other repository errors
	// are wrapped with a method-specific prefix and propagated as
	// non-sentinel errors, so the handler maps them to 500 rather than
	// incorrectly reporting a missing user.
	//
	// The handler maps this to 404 Not Found.
	ErrUserNotFound = errors.New("user not found")

	// ErrDeviceTokenNotFound is returned by every device-token method that
	// performs an initial lookup by token ID — UpdateDeviceToken,
	// DeleteDeviceToken, and ensureDeviceTokenOwnership — when the token
	// ID does not resolve to a row. As with ErrUserNotFound, the sentinel
	// is produced only for pgx.ErrNoRows; any other repository failure
	// from the initial lookup is wrapped and served as a 500.
	//
	// The handler maps this to 404 Not Found.
	ErrDeviceTokenNotFound = errors.New("device token not found")

	// ErrDeviceTokenForbidden is returned when an authenticated mobile user
	// attempts to update or delete a device token owned by another user.
	// The handler maps it to 403 Forbidden without disclosing the token owner.
	ErrDeviceTokenForbidden = errors.New("device token does not belong to authenticated user")

	// ErrDeviceTokenDuplicate is returned by RegisterDeviceToken and
	// UpdateDeviceToken when the supplied push token is already
	// registered. The check that produces it — deviceRepo.ExistsByPushToken
	// — is advisory, not atomic; two concurrent requests with the same
	// push token can both observe "not taken" and both proceed to the
	// write, with the outcome determined by the schema's unique
	// constraint. See the two methods for details.
	//
	// Note that RegisterDeviceToken treats a same-user re-registration
	// as a duplicate, unlike the mobile feature's equivalent upsert; see
	// RegisterDeviceToken for the divergence.
	//
	// The handler maps this to 409 Conflict.
	ErrDeviceTokenDuplicate = errors.New("device token already exists")

	// ErrInvalidPlatform is returned by RegisterDeviceToken and
	// UpdateDeviceToken when the platform string is not one of "ANDROID",
	// "IOS", or "WEB". The comparison is case-sensitive against those
	// three exact uppercase literals.
	//
	// The handler maps this to 400 Bad Request.
	ErrInvalidPlatform = errors.New("invalid platform: must be ANDROID, IOS, or WEB")
)

// Transactor is the minimal contract that UserService requires in order to
// wrap a multi-write operation in a single database transaction.
//
// The interface is deliberately narrow: it exposes only the ability to
// begin a transaction, and returns a pgx.Tx that the service can hand to
// sqlc.New to obtain a transaction-scoped querier. This keeps the users
// feature decoupled from pgxpool and lets a test supply a stub Transactor
// without touching a live database.
//
// # Contract
//
// Begin returns a non-nil transaction on success. The caller is
// responsible for exactly one of Commit or Rollback on the returned
// transaction; a transaction that is neither committed nor rolled back
// holds its connection until the process exits or the database closes
// it. The conventional Go pattern — Begin, defer Rollback, Commit last —
// is safe because Rollback after a successful Commit is a no-op that
// returns pgx.ErrTxClosed, which callers should discard.
//
// # Implementation
//
// *pgxpool.Pool satisfies this interface structurally, so production
// wiring passes the shared pool directly. A test may pass any value with
// a compatible Begin method.
type Transactor interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// UserService provides business logic for user and device-token management.
//
// It is the service layer shared by two handlers — UserHandler for the
// administrative read endpoints and DeviceTokenHandler for the
// device-token CRUD surface. The service covers four logical areas:
//
//   - Read paths over the users table (List, GetByID, Search).
//   - Device-token CRUD (ListDeviceTokens, RegisterDeviceToken,
//     UpdateDeviceToken, DeleteDeviceToken).
//   - User-to-segment memberships (GetUserSegments), a read-only
//     projection over the segment_members table.
//   - User notification history (GetUserNotifications), a read-only
//     projection over notification_deliveries.
//
// # Dependency style
//
// Unlike the staff feature, which depends on interfaces, and the segments
// feature, which uses interfaces for repositories, this service depends
// on the concrete repository types directly. The five *repository.*
// fields are concrete pointers, not interfaces. The trade-off is the same
// as elsewhere in the codebase: less testability, fewer moving parts.
// The users feature has no unit tests today, so the missing seam is not
// currently exercised.
//
// The Transactor dependency is an exception to that pattern: it is
// declared as an interface so that tests can substitute a stub, and so
// that the service is not coupled to pgxpool.
//
// # Error handling
//
// Every by-ID lookup in this service follows the pattern established by
// GetByID: pgx.ErrNoRows is collapsed into the relevant not-found
// sentinel, while any other repository failure is wrapped and propagated
// as a non-sentinel error. The handler distinguishes the two and maps
// them to 404 and 500 respectively, so a transient database problem is
// never reported to a client as a missing record. See the sentinel
// documentation above for the policy statement.
//
// # Transactions
//
// The service owns a Transactor — typically the shared *pgxpool.Pool —
// that it uses to wrap multi-write operations. Currently only
// UpdateDeviceToken needs a transaction: it writes metadata and the
// active flag in two separate repository calls, and the two must commit
// or roll back together. Every other method performs at most one write
// per call and does not open a transaction.
//
// # Concurrency
//
// The service is stateless after construction and safe for concurrent use.
// It holds no per-request state and shares nothing mutable between
// invocations. The five concrete repositories and the Transactor are
// themselves safe for concurrent use; the underlying pgxpool handles
// connection multiplexing and each transaction is bound to a single
// connection.
type UserService struct {
	userRepo     *repository.UserRepository
	deviceRepo   *repository.DeviceTokenRepository
	segmentRepo  *repository.SegmentMemberRepository
	deliveryRepo *repository.NotificationDeliveryRepository
	readRepo     *repository.NotificationReadRepository
	tx           Transactor
}

// NewUserService creates a new UserService instance.
//
// All six parameters must be non-nil. None is validated here; a nil
// repository will panic on the first method call that uses it, and a nil
// Transactor will panic on the first call to UpdateDeviceToken. In
// practice the dependencies are wired by NewUserModule from the shared
// sqlc.Queries and the shared *pgxpool.Pool.
//
// # Unused read repository
//
// The readRepo field is stored but never referenced by any method in
// this file. It appears to be a historical artefact — the notification-
// read projection (which would record that a user acknowledged a
// notification in the in-app inbox) is not exposed through this
// service's endpoints. The field is retained rather than removed per
// Rule 3 of this documentation pass; a future cleanup would drop it
// along with the corresponding module-wiring line.
//
// # Transactor parameter
//
// tx is the shared pool (or a test stub) that the service uses for the
// multi-write UpdateDeviceToken path. Because it is declared as the
// narrow Transactor interface rather than as *pgxpool.Pool, the service
// remains decoupled from the pool implementation and can be tested with
// a stub.
//
// # Concurrency
//
// The constructor is cheap and stateless; the returned service holds only
// the five repository pointers and the Transactor.
func NewUserService(
	userRepo *repository.UserRepository,
	deviceRepo *repository.DeviceTokenRepository,
	segmentRepo *repository.SegmentMemberRepository,
	deliveryRepo *repository.NotificationDeliveryRepository,
	readRepo *repository.NotificationReadRepository,
	tx Transactor,
) *UserService {
	return &UserService{
		userRepo:     userRepo,
		deviceRepo:   deviceRepo,
		segmentRepo:  segmentRepo,
		deliveryRepo: deliveryRepo,
		readRepo:     readRepo,
		tx:           tx,
	}
}

// ============================================
// User Methods
// ============================================

// List returns a paginated list of users with optional filters.
//
// # Filtering
//
// keyword, status, and externalID may be combined. The underlying query
// applies the non-empty filters with AND semantics:
//
//   - keyword searches name, email, and external_id.
//   - status performs an exact status match.
//   - externalID performs an exact external_id match.
//
// An empty filter means “no filter on that column”.
//
// # Pagination
//
// page and limit are applied to the filtered result set as a normal SQL
// LIMIT/OFFSET. The returned total is obtained from CountWithFilters using
// the same filter arguments, so pagination metadata matches the visible
// page.
//
// # Errors
//
// Returns a wrapped error with the prefix "list users: " or
// "count users: " for repository failures. No repository error is collapsed
// into a not-found sentinel on this path; both failures are served as 500
// by the handler.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *UserService) List(ctx context.Context, page, limit int32, keyword, status, externalID string) ([]User, int64, error) {
	offset := (page - 1) * limit

	rows, err := s.userRepo.ListWithFilters(ctx, keyword, status, externalID, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}

	total, err := s.userRepo.CountWithFilters(ctx, keyword, status, externalID)
	if err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	result := make([]User, len(rows))
	for i, u := range rows {
		result[i] = User{
			ID:         u.ID,
			ExternalID: u.ExternalID,
			Name:       u.Name.String,
			Email:      u.Email.String,
			Status:     u.Status,
			CreatedAt:  u.CreatedAt.Time,
			UpdatedAt:  u.UpdatedAt.Time,
		}
	}

	return result, total, nil
}

// GetByID returns a single user by ID.
//
// The lookup reports ErrUserNotFound only when the repository returns
// pgx.ErrNoRows. Any other repository failure — a connectivity error, a
// query failure, or another driver-level problem — is wrapped with
// "get user: " and returned as a non-sentinel error, so the handler maps
// it to 500 instead of incorrectly reporting 404.
//
// # Nullable columns are flattened
//
// The SQL row's Name and Email are pgtype.Text values, which may be NULL.
// The method reads the .String field of each, producing the empty string
// for a NULL column. The domain User therefore cannot distinguish "the
// user has no name" from "the user's name is the empty string"; the two
// cases are equivalent downstream. This matches the flattening applied by
// the mobile feature's user-upsert path.
//
// # Errors
//
// Returns ErrUserNotFound only when the underlying lookup produces
// pgx.ErrNoRows. Other lookup failures are wrapped with "get user: " and
// are not sentinels; the handler maps them to 500.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *UserService) GetByID(ctx context.Context, id int64) (*User, error) {
	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user: %w", err)
	}

	return &User{
		ID:         user.ID,
		ExternalID: user.ExternalID,
		Name:       user.Name.String,
		Email:      user.Email.String,
		Status:     user.Status,
		CreatedAt:  user.CreatedAt.Time,
		UpdatedAt:  user.UpdatedAt.Time,
	}, nil
}

// Search returns a minimal list of users for autocomplete/typeahead.
//
// # Hard-coded result cap
//
// The method calls userRepo.Search with a fixed offset of 0 and a fixed
// limit of 20. There is no way for a caller to control the cap through
// this method's signature, and the handler does not attempt to pass a
// limit through. A search that matches more than 20 users returns only
// the first 20; the client has no way to paginate to the rest or to know
// that a truncation occurred. The endpoint is therefore appropriate for
// typeahead / autocomplete use cases, where the caller refines the
// keyword until the result set is small, but not for bulk exports.
//
// # Minimal projection
//
// The returned SearchUserResult carries only ID, ExternalID, and Name.
// The user's email is deliberately excluded so that a search response
// cannot be used to enumerate the user base's email addresses. A caller
// that needs the email must follow up with a GetByID call.
//
// # Errors
//
// Returns a wrapped error with the prefix "search users: " for a
// repository failure. This is not a sentinel, so the handler serves it
// through its default 500 branch.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *UserService) Search(ctx context.Context, keyword string) ([]SearchUserResult, error) {
	users, err := s.userRepo.Search(ctx, keyword, 0, 20)
	if err != nil {
		return nil, fmt.Errorf("search users: %w", err)
	}

	result := make([]SearchUserResult, len(users))
	for i, u := range users {
		result[i] = SearchUserResult{
			ID:         u.ID,
			ExternalID: u.ExternalID,
			Name:       u.Name.String,
		}
	}

	return result, nil
}

// ============================================
// Device Token Methods
// ============================================

// ListDeviceTokens returns all device tokens for a user.
//
// # Existence check
//
// The method performs a FindByID lookup on the user before fetching the
// tokens, so a call for a non-existent user is rejected with
// ErrUserNotFound rather than returning an empty list. Only pgx.ErrNoRows
// from that lookup produces ErrUserNotFound; any other repository failure
// is wrapped with "get user: " and served as a 500, so a transient
// database problem is never reported to the client as a missing user.
//
// The existence check and the token fetch are not transactional: a user
// deleted between the two reads produces an empty list rather than an
// error, which is harmless in practice.
//
// # Hard-coded result cap
//
// The underlying repository call is made with a fixed offset of 0 and a
// fixed limit of 100. There is no pagination. A user with more than 100
// active device tokens will have the excess silently dropped; the
// response carries no indication that truncation occurred. The cap is
// well above realistic device counts, but it is not enforced or
// documented to the client.
//
// # Nullable columns are flattened
//
// The row's InstallationID, AppVersion, OSVersion, and DeviceModel are
// pgtype.Text values. The method reads .String from each, producing an
// empty string for a NULL column. The domain DeviceToken therefore
// cannot distinguish "not recorded" from "recorded as empty string".
//
// # Errors
//
// Returns ErrUserNotFound when the user lookup produces pgx.ErrNoRows.
// Returns a wrapped "get user: " error for any other failure of that
// lookup, and a wrapped "list device tokens: " error for a failure of
// the token fetch. Neither wrapped error is a sentinel, so the handler
// serves them through its default 500 branch.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *UserService) ListDeviceTokens(ctx context.Context, userID int64) ([]DeviceToken, error) {
	_, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user: %w", err)
	}

	rows, err := s.deviceRepo.ListByUser(ctx, userID, 0, 100)
	if err != nil {
		return nil, fmt.Errorf("list device tokens: %w", err)
	}

	result := make([]DeviceToken, len(rows))
	for i, row := range rows {
		var lastSeen *time.Time
		if row.LastSeenAt.Valid {
			lastSeen = &row.LastSeenAt.Time
		}
		result[i] = DeviceToken{
			ID:             row.ID,
			UserID:         row.UserID,
			Provider:       row.Provider,
			Platform:       row.Platform,
			InstallationID: row.InstallationID.String,
			PushToken:      row.PushToken,
			AppVersion:     row.AppVersion.String,
			OSVersion:      row.OsVersion.String,
			DeviceModel:    row.DeviceModel.String,
			IsActive:       row.IsActive,
			LastSeenAt:     lastSeen,
			CreatedAt:      row.CreatedAt.Time,
			UpdatedAt:      row.UpdatedAt.Time,
		}
	}

	return result, nil
}

// RegisterDeviceToken registers a new device token for a user.
//
// # Flow
//
//  1. Validate the platform string against the three accepted literals
//     ("ANDROID", "IOS", "WEB"); a mismatch returns ErrInvalidPlatform
//     before any database work is performed.
//  2. Verify the user exists via userRepo.FindByID. Only pgx.ErrNoRows
//     produces ErrUserNotFound; any other lookup failure is wrapped with
//     "get user: " and served as a 500.
//  3. Check whether the push token is already registered via
//     deviceRepo.ExistsByPushToken. A match returns
//     ErrDeviceTokenDuplicate — including when the existing row belongs
//     to the same user; see the divergence note below.
//  4. Map the platform to a provider: ANDROID and WEB both map to "FCM",
//     IOS maps to "APNS".
//  5. Translate the optional *string fields to pgtype.Text — nil becomes
//     NULL, a non-nil pointer becomes a non-NULL string.
//  6. Insert the row and return the domain view.
//
// # Create-only, no upsert
//
// Unlike the mobile feature's registerDeviceToken, this method does not
// perform an upsert. If a device with the same push token is already
// registered — even for the same user — the call fails with
// ErrDeviceTokenDuplicate. A client that re-runs registration as an
// idempotent "ensure this device is registered" operation will therefore
// receive a 409 on the second attempt rather than a 201 with the existing
// row.
//
// The mobile feature's flow is the recommended one for client-initiated
// registration because it handles the "app relaunched, same device"
// case; this method is intended for server-to-server provisioning, where
// the caller is expected to know whether the device is already
// registered.
//
// # Advisory duplicate check
//
// The ExistsByPushToken check at step 3 and the insert at step 6 are not
// atomic. Two concurrent registrations with the same push token will both
// observe "not taken" and both attempt the insert; the outcome depends on
// whether the schema enforces a UNIQUE constraint on push_token. If it
// does, the second insert fails with a unique-violation error, wrapped
// with "create device token: " and served as a 500 — not the 409 that
// ErrDeviceTokenDuplicate would have produced. If the schema does not
// enforce uniqueness, both inserts succeed and the table contains two
// rows with the same push token.
//
// # No ownership transfer
//
// A push token that was previously registered to user A cannot be
// re-registered to user B through this method: the duplicate check
// refuses it before the insert. This is a stricter policy than the mobile
// feature's upsert, which would leave the row's user_id pointing at the
// original owner in the same scenario. Neither policy is clearly correct
// — the device-handover case is genuinely ambiguous — but the two
// features currently disagree, which is worth knowing when reasoning
// about the system as a whole.
//
// # Errors
//
// Returns ErrInvalidPlatform, ErrUserNotFound, or ErrDeviceTokenDuplicate
// for the corresponding conditions. Returns a wrapped error with the
// prefix "get user: ", "check push token: ", or "create device token: "
// for repository failures; none is a sentinel, so the handler serves them
// through its default 500 branch.
//
// # Concurrency
//
// Safe for concurrent use. See the advisory-check note above for the
// concurrency implications of the duplicate check.
func (s *UserService) RegisterDeviceToken(ctx context.Context, userID int64, req RegisterDeviceTokenRequest) (*DeviceToken, error) {
	// Validate platform.
	if req.Platform != "ANDROID" && req.Platform != "IOS" && req.Platform != "WEB" {
		return nil, ErrInvalidPlatform
	}

	// Check if user exists.
	_, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user: %w", err)
	}

	// Check for duplicate push_token.
	exists, err := s.deviceRepo.ExistsByPushToken(ctx, req.PushToken)
	if err != nil {
		return nil, fmt.Errorf("check push token: %w", err)
	}
	if exists {
		return nil, ErrDeviceTokenDuplicate
	}

	// Map provider based on platform.
	var provider string
	switch req.Platform {
	case "ANDROID":
		provider = "FCM"
	case "IOS":
		provider = "APNS"
	case "WEB":
		provider = "FCM"
	}

	var installationID, appVersion, osVersion, deviceModel pgtype.Text
	if req.InstallationID != nil {
		installationID = pgtype.Text{String: *req.InstallationID, Valid: true}
	}
	if req.AppVersion != nil {
		appVersion = pgtype.Text{String: *req.AppVersion, Valid: true}
	}
	if req.OSVersion != nil {
		osVersion = pgtype.Text{String: *req.OSVersion, Valid: true}
	}
	if req.DeviceModel != nil {
		deviceModel = pgtype.Text{String: *req.DeviceModel, Valid: true}
	}

	row, err := s.deviceRepo.Create(ctx, sqlc.CreateDeviceTokenParams{
		UserID:         userID,
		Platform:       req.Platform,
		InstallationID: installationID,
		PushToken:      req.PushToken,
		Provider:       provider,
		AppVersion:     appVersion,
		OsVersion:      osVersion,
		DeviceModel:    deviceModel,
	})
	if err != nil {
		return nil, fmt.Errorf("create device token: %w", err)
	}

	var lastSeen *time.Time
	if row.LastSeenAt.Valid {
		lastSeen = &row.LastSeenAt.Time
	}

	return &DeviceToken{
		ID:             row.ID,
		UserID:         row.UserID,
		Provider:       row.Provider,
		Platform:       row.Platform,
		InstallationID: row.InstallationID.String,
		PushToken:      row.PushToken,
		AppVersion:     row.AppVersion.String,
		OSVersion:      row.OsVersion.String,
		DeviceModel:    row.DeviceModel.String,
		IsActive:       row.IsActive,
		LastSeenAt:     lastSeen,
		CreatedAt:      row.CreatedAt.Time,
		UpdatedAt:      row.UpdatedAt.Time,
	}, nil
}

// RegisterDeviceTokenForExternalID registers a device token for the mobile
// user identified by externalID.
//
// externalID must originate from a verified mobile JWT, never directly from
// client input. The method resolves it to the internal users.id and delegates
// validation and persistence to RegisterDeviceToken. It returns ErrUserNotFound
// when no local user matches externalID (pgx.ErrNoRows from the lookup); a
// genuine lookup failure is wrapped and propagated as a non-sentinel error.
// All errors returned by the delegate are preserved unchanged. The service
// is safe for concurrent use.
func (s *UserService) RegisterDeviceTokenForExternalID(ctx context.Context, externalID string, req RegisterDeviceTokenRequest) (*DeviceToken, error) {
	userID, err := s.userIDByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}
	return s.RegisterDeviceToken(ctx, userID, req)
}

// UpdateDeviceToken updates a device token.
//
// # Atomicity
//
// The method updates two logically distinct pieces of state: the token's
// metadata (platform, push token, app version, OS version, device model,
// installation ID) and its active flag. Both updates are performed inside
// a single database transaction, so either both commit or neither does.
//
// This closes finding C20 in code-review.md, where the two writes were
// issued as independent repository calls: a failure of the second write
// left the row with updated metadata but an unchanged active flag, and
// the caller received an error that named only the second step. With the
// transaction in place, a failure of the status write rolls back the
// metadata write, and the client can retry the whole operation without
// worrying about a partially-applied state.
//
// The read (FindByID) and the duplicate check (ExistsByPushToken) are
// also executed inside the transaction, so they observe the same
// database snapshot as the writes that follow. This prevents the race in
// which the row is deleted between the existence check and the write:
// the write would otherwise be a silent no-op that returned success.
//
// # Flow
//
//  1. Begin a transaction via s.tx. A failure to begin returns a wrapped
//     "begin transaction: " error.
//  2. Build a transaction-scoped DeviceTokenRepository from sqlc.New(tx)
//     so that all subsequent calls run on the transaction's connection
//     rather than on the shared pool.
//  3. Load the existing token by ID within the transaction. Only
//     pgx.ErrNoRows produces ErrDeviceTokenNotFound; any other lookup
//     failure is wrapped with "get device token: " and served as a 500.
//  4. If req.PushToken is non-nil AND differs from the current push
//     token, check for a duplicate via ExistsByPushToken. A match returns
//     ErrDeviceTokenDuplicate. A same-value update is skipped without a
//     check, so a client resending the current push token does not
//     trigger a spurious conflict.
//  5. Build an update struct pre-populated with the existing values, so
//     that fields not supplied by the caller retain their current state.
//  6. Apply each non-nil field. Platform is validated against the
//     accepted literals, and a platform change recomputes the provider
//     mapping (ANDROID/WEB → FCM, IOS → APNS). A platform that is not
//     changed leaves the existing provider untouched, so the two fields
//     are not independently settable through this method.
//  7. Issue the metadata update via UpdateFull on the transaction-scoped
//     repository.
//  8. If req.IsActive is non-nil, issue the status update via
//     UpdateStatus on the same transaction-scoped repository.
//  9. Commit the transaction. A commit failure returns a wrapped
//     "commit transaction: " error and the caller is expected to treat
//     the whole operation as failed.
//
// # Deferred rollback
//
// The method registers a deferred Rollback immediately after Begin. The
// deferred call is a no-op when the transaction commits successfully —
// pgx returns ErrTxClosed and the error is discarded — and an actual
// rollback on any earlier return path. This is the standard Go pattern
// for transaction handling and ensures the connection is always released.
//
// # Advisory duplicate check
//
// The ExistsByPushToken check at step 4 runs inside the transaction but
// against a snapshot that the transaction does not lock. Two concurrent
// updates claiming the same push token can both pass the check; the
// outcome depends on the schema's unique constraint, as documented on
// ErrDeviceTokenDuplicate. The transaction does not change this property
// — an advisory uniqueness check is not made authoritative by being
// wrapped in a transaction, because the conflicting row may not yet be
// visible to the snapshot.
//
// # Errors
//
// Returns ErrDeviceTokenNotFound only when the initial token lookup
// produces pgx.ErrNoRows, ErrDeviceTokenDuplicate for a push-token
// collision, or ErrInvalidPlatform for an unsupported platform literal.
// Returns a wrapped error with the prefix "begin transaction: ",
// "get device token: ", "check push token: ", "update device token: ",
// "update device token status: ", or "commit transaction: " for
// repository and transaction failures. None of the wrapped errors is a
// sentinel, so the handler serves them through its default 500 branch.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent updates on the same token are
// not serialised by this method — PostgreSQL's default isolation level
// (read committed) does not prevent two transactions from reading the
// same row and then writing it — but the writes within each call are
// atomic with respect to each other. The final state reflects whichever
// transaction commits last.
func (s *UserService) UpdateDeviceToken(ctx context.Context, tokenID int64, req UpdateDeviceTokenRequest) error {
	// Begin a transaction so that the metadata write and the status write
	// commit or roll back together. All reads are performed inside the
	// same transaction so they observe a consistent snapshot.
	tx, err := s.tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		// Rollback is a no-op after a successful Commit — pgx returns
		// ErrTxClosed and the error is discarded deliberately. On any
		// earlier return path this call actually rolls back the open
		// transaction and releases the connection.
		_ = tx.Rollback(ctx)
	}()

	txDeviceRepo := repository.NewDeviceTokenRepository(sqlc.New(tx))

	existing, err := txDeviceRepo.FindByID(ctx, tokenID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDeviceTokenNotFound
		}
		return fmt.Errorf("get device token: %w", err)
	}

	// Check duplicate push_token if changed.
	if req.PushToken != nil && *req.PushToken != existing.PushToken {
		exists, err := txDeviceRepo.ExistsByPushToken(ctx, *req.PushToken)
		if err != nil {
			return fmt.Errorf("check push token: %w", err)
		}
		if exists {
			return ErrDeviceTokenDuplicate
		}
	}

	// Build update params.
	update := sqlc.UpdateDeviceTokenFullParams{
		ID:             tokenID,
		Platform:       existing.Platform,
		InstallationID: existing.InstallationID,
		PushToken:      existing.PushToken,
		Provider:       existing.Provider,
		AppVersion:     existing.AppVersion,
		OsVersion:      existing.OsVersion,
		DeviceModel:    existing.DeviceModel,
	}

	if req.Platform != nil {
		if *req.Platform != "ANDROID" && *req.Platform != "IOS" && *req.Platform != "WEB" {
			return ErrInvalidPlatform
		}
		update.Platform = *req.Platform
		switch *req.Platform {
		case "ANDROID", "WEB":
			update.Provider = "FCM"
		case "IOS":
			update.Provider = "APNS"
		}
	}
	if req.PushToken != nil {
		update.PushToken = *req.PushToken
	}
	if req.InstallationID != nil {
		update.InstallationID = pgtype.Text{String: *req.InstallationID, Valid: true}
	}
	if req.AppVersion != nil {
		update.AppVersion = pgtype.Text{String: *req.AppVersion, Valid: true}
	}
	if req.OSVersion != nil {
		update.OsVersion = pgtype.Text{String: *req.OSVersion, Valid: true}
	}
	if req.DeviceModel != nil {
		update.DeviceModel = pgtype.Text{String: *req.DeviceModel, Valid: true}
	}

	if err := txDeviceRepo.UpdateFull(ctx, update); err != nil {
		return fmt.Errorf("update device token: %w", err)
	}

	if req.IsActive != nil {
		if err := txDeviceRepo.UpdateStatus(ctx, sqlc.UpdateDeviceTokenStatusParams{
			ID:       tokenID,
			IsActive: *req.IsActive,
		}); err != nil {
			return fmt.Errorf("update device token status: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// UpdateDeviceTokenForExternalID updates a device token only when externalID
// identifies the token's current owner.
//
// externalID must come from verified mobile authentication. The method returns
// ErrUserNotFound, ErrDeviceTokenNotFound, or ErrDeviceTokenForbidden before
// applying an update; successful ownership verification delegates to
// UpdateDeviceToken and preserves its validation and repository errors. The
// not-found sentinels are produced only for pgx.ErrNoRows; other lookup
// failures are wrapped and propagated as non-sentinel errors. It is safe for
// concurrent use.
//
// # Not atomic with the ownership check
//
// The ownership check and the delegated update are two separate operations
// without an enclosing transaction. A concurrent change of the token's
// owner between the two would allow the update to be applied to a token
// that has since been reassigned — an edge case that is not exploitable
// in practice because ownership changes on this table are not supported
// by any current write path, but worth knowing when reasoning about the
// method.
func (s *UserService) UpdateDeviceTokenForExternalID(ctx context.Context, externalID string, tokenID int64, req UpdateDeviceTokenRequest) error {
	if err := s.ensureDeviceTokenOwnership(ctx, externalID, tokenID); err != nil {
		return err
	}
	return s.UpdateDeviceToken(ctx, tokenID, req)
}

// DeleteDeviceTokenForExternalID deletes a device token only when externalID
// identifies the token's current owner.
//
// externalID must come from verified mobile authentication. The method returns
// ErrUserNotFound, ErrDeviceTokenNotFound, or ErrDeviceTokenForbidden when the
// ownership check cannot pass; otherwise it deletes the token. The not-found
// sentinels are produced only for pgx.ErrNoRows; other lookup failures are
// wrapped and propagated as non-sentinel errors. It is safe for concurrent
// use.
func (s *UserService) DeleteDeviceTokenForExternalID(ctx context.Context, externalID string, tokenID int64) error {
	if err := s.ensureDeviceTokenOwnership(ctx, externalID, tokenID); err != nil {
		return err
	}
	return s.deviceRepo.Delete(ctx, tokenID)
}

// userIDByExternalID resolves a verified mobile external ID to users.id.
//
// It returns ErrUserNotFound only when the repository lookup produces
// pgx.ErrNoRows — the external ID does not correspond to a local user.
// Any other repository failure is wrapped with "get user by external id: "
// and propagated as a non-sentinel error, so a transient database problem
// is served as a 500 rather than being reported to the caller as a
// missing user. This mirrors the error-handling policy applied by the
// other by-ID lookups in this file.
//
// It has no mutable state and is safe for concurrent use.
func (s *UserService) userIDByExternalID(ctx context.Context, externalID string) (int64, error) {
	user, err := s.userRepo.FindByExternalID(ctx, externalID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrUserNotFound
		}
		return 0, fmt.Errorf("get user by external id: %w", err)
	}
	return user.ID, nil
}

// ensureDeviceTokenOwnership verifies that externalID resolves to the owner
// of tokenID.
//
// The method intentionally compares internal IDs after resolving the verified
// external ID, so a client never controls either side of the authorization
// decision.
//
// # Error semantics
//
// The method distinguishes three outcomes:
//
//   - The external ID does not resolve to a local user: ErrUserNotFound
//     is returned only when the lookup produces pgx.ErrNoRows; other
//     failures are wrapped by userIDByExternalID and propagated.
//   - The token ID does not resolve to a row: ErrDeviceTokenNotFound is
//     returned only when the token lookup produces pgx.ErrNoRows; any
//     other failure is wrapped with "get device token: " and served as
//     a 500.
//   - The token exists but is owned by a different user:
//     ErrDeviceTokenForbidden is returned, without disclosing the actual
//     owner to the caller.
//
// It is safe for concurrent use.
func (s *UserService) ensureDeviceTokenOwnership(ctx context.Context, externalID string, tokenID int64) error {
	userID, err := s.userIDByExternalID(ctx, externalID)
	if err != nil {
		return err
	}
	token, err := s.deviceRepo.FindByID(ctx, tokenID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDeviceTokenNotFound
		}
		return fmt.Errorf("get device token: %w", err)
	}
	if token.UserID != userID {
		return ErrDeviceTokenForbidden
	}
	return nil
}

// ============================================
// User Segments Methods
// ============================================

// GetUserSegments returns all segments a user belongs to.
//
// # Existence check
//
// The method performs a FindByID lookup on the user before fetching the
// segments, so a call for a non-existent user is rejected with
// ErrUserNotFound rather than returning an empty list. Only pgx.ErrNoRows
// from that lookup produces the sentinel; any other failure is wrapped
// with "get user: " and served as a 500.
//
// The check is not atomic with the segment fetch, but the race is
// harmless — a user deleted between the two reads produces an empty list.
//
// # Hard-coded result cap
//
// The underlying repository call uses a fixed offset of 0 and a fixed
// limit of 100. As with ListDeviceTokens, there is no pagination and no
// truncation indicator; a user who belongs to more than 100 segments will
// have the excess silently dropped.
//
// # Errors
//
// Returns ErrUserNotFound only when the user lookup produces
// pgx.ErrNoRows. A wrapped "get user: " error is returned for any other
// failure of that lookup, and a wrapped "list segments by user: " error
// is returned for a failure of the segment fetch. Neither wrapped error
// is a sentinel, so the handler serves them through its default 500
// branch.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *UserService) GetUserSegments(ctx context.Context, userID int64) ([]UserSegment, error) {
	_, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user: %w", err)
	}

	rows, err := s.segmentRepo.ListSegmentsByUser(ctx, userID, 0, 100)
	if err != nil {
		return nil, fmt.Errorf("list segments by user: %w", err)
	}

	result := make([]UserSegment, len(rows))
	for i, row := range rows {
		result[i] = UserSegment{
			ID:   row.ID,
			Name: row.Name,
		}
	}

	return result, nil
}

// ============================================
// User Notification History Methods
// ============================================

// GetUserNotifications returns notification history for a user with pagination.
//
// # One row per notification
//
// The underlying repository call ListByUser returns one row per distinct
// notification for the user. The repository collapses the
// notification_deliveries rows that belong to a multi-device user into a
// single representative row per notification, choosing the representative
// delivery by status priority (OPENED > DELIVERED > SENT > FAILED). A user
// with three devices who has received ten notifications therefore sees ten
// entries, not thirty, and the Total returned by this method matches the
// number of items the caller can page through.
//
// The status on each returned UserNotification reflects the priority-selected
// delivery; a notification opened on any device is reported as OPENED even
// if another device's delivery is still in DELIVERED or FAILED. A user who
// has never opened the notification sees the status of the most advanced
// non-opened delivery instead. The rationale for the priority is documented
// on the repository's ListByUser; it is the mobile inbox's semantics, not
// the admin analytics view's.
//
// # Existence check
//
// The method performs a FindByID lookup on the user before fetching the
// history, so a call for a non-existent user is rejected with
// ErrUserNotFound rather than returning an empty list. Only
// pgx.ErrNoRows from that lookup produces the sentinel; any other
// failure is wrapped with "get user: " and served as a 500.
//
// # OpenedAt formatting
//
// The underlying row's OpenedAt is a pgtype.Timestamptz. When valid, the
// method formats it to RFC3339 and stores the result as a *string on the
// domain model. This is the only place in the codebase where a timestamp
// is formatted at the service layer rather than the handler; the domain
// model and DTO both carry the value as *string, matching the shape
// documented on UserNotification.
//
// # Errors
//
// Returns ErrUserNotFound only when the user lookup produces
// pgx.ErrNoRows. A wrapped "get user: " error is returned for any other
// failure of that lookup. Returns a wrapped "list user notifications: "
// or "count user notifications: " error for repository failures on the
// history fetch. None of the wrapped errors is a sentinel, so the handler
// serves them through its default 500 branch.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *UserService) GetUserNotifications(ctx context.Context, userID int64, page, limit int32) ([]UserNotification, int64, error) {
	_, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, ErrUserNotFound
		}
		return nil, 0, fmt.Errorf("get user: %w", err)
	}

	offset := (page - 1) * limit
	rows, err := s.deliveryRepo.ListByUser(ctx, userID, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list user notifications: %w", err)
	}

	total, err := s.deliveryRepo.CountByUser(ctx, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("count user notifications: %w", err)
	}

	result := make([]UserNotification, len(rows))
	for i, row := range rows {
		var openedAt *string
		if row.OpenedAt.Valid {
			val := row.OpenedAt.Time.Format(time.RFC3339)
			openedAt = &val
		}
		result[i] = UserNotification{
			NotificationID: row.NotificationID,
			Title:          row.Title,
			Status:         row.Status,
			OpenedAt:       openedAt,
		}
	}

	return result, total, nil
}
