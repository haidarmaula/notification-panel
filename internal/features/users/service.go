package users

import (
	"context"
	"errors"
	"fmt"
	"time"

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
var (
	// ErrUserNotFound is returned when a user ID or external_id does not
	// resolve to a row. It is also returned for *any* repository error
	// from a by-ID lookup — a database connectivity failure surfaces as
	// a 404 rather than a 500. The one exception is the external_id fast
	// path in List, which swallows the error entirely; see List for the
	// details.
	//
	// The handler maps this to 404 Not Found.
	ErrUserNotFound = errors.New("user not found")

	// ErrDeviceTokenNotFound is returned by UpdateDeviceToken and
	// DeleteDeviceToken when the token ID does not resolve to a row. As
	// with ErrUserNotFound, any repository failure from the initial
	// lookup produces this sentinel, so a database error is reported as
	// a 404 rather than a 500.
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
// # Concurrency
//
// The service is stateless after construction and safe for concurrent use.
// It holds no per-request state and shares nothing mutable between
// invocations. The five concrete repositories are themselves safe for
// concurrent use; the underlying pgxpool handles connection multiplexing.
type UserService struct {
	userRepo     *repository.UserRepository
	deviceRepo   *repository.DeviceTokenRepository
	segmentRepo  *repository.SegmentMemberRepository
	deliveryRepo *repository.NotificationDeliveryRepository
	readRepo     *repository.NotificationReadRepository
}

// NewUserService creates a new UserService instance.
//
// All five parameters must be non-nil; none is validated, so a nil
// repository will panic on the first method call that uses it rather than
// at construction. In practice the dependencies are wired by
// NewUserModule from the shared sqlc.Queries.
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
// # Concurrency
//
// The constructor is cheap and stateless; the returned service holds only
// the five repository pointers.
func NewUserService(
	userRepo *repository.UserRepository,
	deviceRepo *repository.DeviceTokenRepository,
	segmentRepo *repository.SegmentMemberRepository,
	deliveryRepo *repository.NotificationDeliveryRepository,
	readRepo *repository.NotificationReadRepository,
) *UserService {
	return &UserService{
		userRepo:     userRepo,
		deviceRepo:   deviceRepo,
		segmentRepo:  segmentRepo,
		deliveryRepo: deliveryRepo,
		readRepo:     readRepo,
	}
}

// ============================================
// User Methods
// ============================================

// List returns a paginated list of users with optional filters.
// Filters: keyword (search by name/email/external_id), status, external_id.
//
// # Filter precedence — filters do not compose
//
// The three filters are not combined. The method selects exactly one
// repository call based on which filter is non-empty, evaluated in a
// fixed priority order:
//
//  1. If externalID is non-empty, the method performs a single-row lookup
//     by external_id. See the fast-path caveat below.
//  2. Otherwise, if keyword is non-empty, the Search query is used.
//  3. Otherwise, if status is non-empty, the status-filtered query is
//     used.
//  4. Otherwise, the unfiltered List query is used.
//
// A caller that supplies both status and keyword will therefore get only
// the keyword-filtered result; the status argument is silently ignored.
// Combining filters requires extending the repository layer with a query
// that accepts multiple predicates, which does not exist today. The
// handler passes all three values through unchanged, so the precedence
// is enforced here, not at the HTTP boundary.
//
// # The external_id fast path swallows repository errors
//
// The first branch is implemented as:
//
//	user, err := s.userRepo.FindByExternalID(ctx, externalID)
//	if err != nil {
//	    return []User{}, 0, nil
//	}
//
// Any error from FindByExternalID — including a database connectivity
// failure, a query error, or an unexpected driver error — is treated the
// same as "no matching row": the method returns an empty slice, a zero
// total, and a nil error. The client therefore sees a successful 200
// response with no results, indistinguishable from a legitimate "no such
// external_id".
//
// A correct implementation would distinguish pgx.ErrNoRows (the "not
// found" case) from other errors and propagate the latter. The current
// code does not, and the failure mode is invisible unless the caller
// inspects the underlying database logs.
//
// # The external_id fast path ignores pagination
//
// When the fast path is taken, the page and limit arguments are ignored
// entirely. A caller that supplies external_id=X&page=5&limit=50 will
// receive the matching user (or nothing) with a hardcoded total of 1,
// regardless of the pagination parameters. The handler echoes the
// caller's page and limit into the response, so the response's
// Pagination metadata may claim "page 5 of 1" — an inconsistency the
// client must tolerate or explicitly avoid by not passing external_id
// alongside pagination arguments.
//
// # Incorrect total on the keyword path
//
// The keyword branch computes the total via userRepo.Count(ctx) — the
// *unfiltered* count — rather than a keyword-aware count. A search
// returning 1 matching user out of 50,000 reports a total of 50,000, and
// a client rendering "page X of Y" pagination will produce an incorrect
// page count. The same defect exists in the staff and segments features;
// the status branch is correctly paired with CountByStatus and does not
// have this issue.
//
// # Errors
//
// Returns a wrapped error with the prefix "search users: ", "list users
// by status: ", "list users: ", "count users: ", or "count users by
// status: " for failures of the corresponding repository call. None of
// these is a sentinel, so the handler serves them through its default
// 500 branch — except on the external_id fast path, where the error is
// swallowed (see above).
//
// # Concurrency
//
// Safe for concurrent use.
func (s *UserService) List(ctx context.Context, page, limit int32, keyword, status, externalID string) ([]User, int64, error) {
	offset := (page - 1) * limit

	var users []sqlc.User
	var total int64
	var err error

	// Fast path: exact external_id match.
	if externalID != "" {
		user, err := s.userRepo.FindByExternalID(ctx, externalID)
		if err != nil {
			return []User{}, 0, nil
		}
		users = []sqlc.User{user}
		total = 1
	} else if keyword != "" {
		users, err = s.userRepo.Search(ctx, keyword, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("search users: %w", err)
		}
		total, err = s.userRepo.Count(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("count users: %w", err)
		}
	} else if status != "" {
		users, err = s.userRepo.ListByStatus(ctx, status, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("list users by status: %w", err)
		}
		total, err = s.userRepo.CountByStatus(ctx, status)
		if err != nil {
			return nil, 0, fmt.Errorf("count users by status: %w", err)
		}
	} else {
		users, err = s.userRepo.List(ctx, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("list users: %w", err)
		}
		total, err = s.userRepo.Count(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("count users: %w", err)
		}
	}

	result := make([]User, len(users))
	for i, u := range users {
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
// The lookup collapses every repository error — including a transient
// database failure — into ErrUserNotFound, so a client cannot distinguish
// "the user does not exist" from "the lookup failed" through the return
// value alone.
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
// Returns ErrUserNotFound for any failure of the underlying lookup. The
// handler maps it to 404.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *UserService) GetByID(ctx context.Context, id int64) (*User, error) {
	user, err := s.userRepo.FindByID(ctx, id)
	if err != nil {
		return nil, ErrUserNotFound
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
// ErrUserNotFound rather than returning an empty list. The two operations
// are not transactional: a user deleted between the lookup and the token
// fetch will produce an empty list rather than an error, which is
// harmless in practice.
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
// Returns ErrUserNotFound if the user does not exist. Returns a wrapped
// "list device tokens: " error for a repository failure on the token
// fetch. The latter is not a sentinel, so the handler serves it through
// its default 500 branch.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *UserService) ListDeviceTokens(ctx context.Context, userID int64) ([]DeviceToken, error) {
	_, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, ErrUserNotFound
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
//  2. Verify the user exists via userRepo.FindByID. Any failure collapses
//     to ErrUserNotFound.
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
// prefix "check push token: " or "create device token: " for repository
// failures; neither is a sentinel, so the handler serves them through its
// default 500 branch.
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
		return nil, ErrUserNotFound
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
// when no local user matches externalID and otherwise preserves the delegate's
// errors. The service is safe for concurrent use.
func (s *UserService) RegisterDeviceTokenForExternalID(ctx context.Context, externalID string, req RegisterDeviceTokenRequest) (*DeviceToken, error) {
	userID, err := s.userIDByExternalID(ctx, externalID)
	if err != nil {
		return nil, err
	}
	return s.RegisterDeviceToken(ctx, userID, req)
}

// UpdateDeviceToken updates a device token.
//
// # Flow
//
//  1. Load the existing token by ID. Any failure collapses to
//     ErrDeviceTokenNotFound.
//  2. If req.PushToken is non-nil AND differs from the current push
//     token, check for a duplicate via ExistsByPushToken. A match returns
//     ErrDeviceTokenDuplicate. A same-value update is skipped without a
//     check, so a client resending the current push token does not
//     trigger a spurious conflict.
//  3. Build an update struct pre-populated with the existing values, so
//     that fields not supplied by the caller retain their current state.
//  4. Apply each non-nil field. Platform is validated against the
//     accepted literals, and a platform change recomputes the provider
//     mapping (ANDROID/WEB → FCM, IOS → APNS). A platform that is not
//     changed leaves the existing provider untouched, so the two fields
//     are not independently settable through this method.
//  5. Issue the metadata update via UpdateFull.
//  6. If req.IsActive is non-nil, issue a *second* write via
//     UpdateStatus to toggle the active flag. See the transaction note
//     below.
//
// # Two separate writes with no transaction
//
// Steps 5 and 6 are two independent repository calls with no enclosing
// database transaction. If step 5 succeeds and step 6 fails — a transient
// connection drop, a constraint violation on the status column, a
// timeout — the token's metadata has been updated but its active flag has
// not, and the caller receives an error that names only the second write.
//
// A client that treats the returned error as "the update failed" and
// retries will issue a redundant metadata update followed by a retried
// status update. A client that treats the error as "nothing changed"
// will be wrong about the metadata. The correct behaviour would be to
// wrap both writes in a transaction, or to fold the is_active field into
// the UpdateFull query so that only one write is required.
//
// # Advisory duplicate check
//
// The ExistsByPushToken check at step 2 and the UpdateFull call at step 5
// are not atomic. Two concurrent updates claiming the same push token can
// both pass the check; the outcome depends on the schema's unique
// constraint, as documented on ErrDeviceTokenDuplicate.
//
// # Errors
//
// Returns ErrDeviceTokenNotFound, ErrDeviceTokenDuplicate, or
// ErrInvalidPlatform for the corresponding conditions. Returns a wrapped
// error with the prefix "check push token: ", "update device token: ", or
// "update device token status: " for repository failures. None of the
// wrapped errors is a sentinel.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent updates on the same token are
// not serialised; the final state reflects whichever write commits last
// for each of the two write steps independently.
func (s *UserService) UpdateDeviceToken(ctx context.Context, tokenID int64, req UpdateDeviceTokenRequest) error {
	existing, err := s.deviceRepo.FindByID(ctx, tokenID)
	if err != nil {
		return ErrDeviceTokenNotFound
	}

	// Check duplicate push_token if changed.
	if req.PushToken != nil && *req.PushToken != existing.PushToken {
		exists, err := s.deviceRepo.ExistsByPushToken(ctx, *req.PushToken)
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

	if err := s.deviceRepo.UpdateFull(ctx, update); err != nil {
		return fmt.Errorf("update device token: %w", err)
	}

	if req.IsActive != nil {
		if err := s.deviceRepo.UpdateStatus(ctx, sqlc.UpdateDeviceTokenStatusParams{
			ID:       tokenID,
			IsActive: *req.IsActive,
		}); err != nil {
			return fmt.Errorf("update device token status: %w", err)
		}
	}

	return nil
}

// DeleteDeviceToken deletes a device token.
//
// # Flow
//
// The method performs a FindByID existence check before issuing the
// delete, so a call for a non-existent token is rejected with
// ErrDeviceTokenNotFound rather than being a silent no-op. The two
// operations are not transactional; a token deleted between the check
// and the delete produces a successful return, which is harmless.
//
// # Hard delete
//
// This is a hard delete of the device_tokens row. Historical delivery
// records that reference the token are subject to whatever foreign-key
// behaviour the schema declares; the service does not cascade or null out
// references. For retiring a token that the push provider has reported as
// unregistered, prefer toggling is_active via UpdateDeviceToken, which
// preserves the historical delivery trail.
//
// # Errors
//
// Returns ErrDeviceTokenNotFound if the token does not exist, or the raw
// repository error if the delete itself fails. The latter is not a
// sentinel, so the handler serves it through its default 500 branch.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent Deletes on the same ID will
// both pass the initial lookup and both attempt the delete; the second is
// either a no-op or a row-not-found error at the SQL level.
func (s *UserService) DeleteDeviceToken(ctx context.Context, tokenID int64) error {
	_, err := s.deviceRepo.FindByID(ctx, tokenID)
	if err != nil {
		return ErrDeviceTokenNotFound
	}
	return s.deviceRepo.Delete(ctx, tokenID)
}

// UpdateDeviceTokenForExternalID updates a device token only when externalID
// identifies the token's current owner.
//
// externalID must come from verified mobile authentication. The method returns
// ErrUserNotFound, ErrDeviceTokenNotFound, or ErrDeviceTokenForbidden before
// applying an update; successful ownership verification delegates to
// UpdateDeviceToken and preserves its validation and repository errors. It is
// safe for concurrent use.
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
// ownership check cannot pass; otherwise it deletes the token. It is safe for
// concurrent use.
func (s *UserService) DeleteDeviceTokenForExternalID(ctx context.Context, externalID string, tokenID int64) error {
	if err := s.ensureDeviceTokenOwnership(ctx, externalID, tokenID); err != nil {
		return err
	}
	return s.deviceRepo.Delete(ctx, tokenID)
}

// userIDByExternalID resolves a verified mobile external ID to users.id.
//
// It returns ErrUserNotFound for any repository lookup failure, consistent
// with the service's existing by-ID lookup behaviour. It has no mutable state
// and is safe for concurrent use.
func (s *UserService) userIDByExternalID(ctx context.Context, externalID string) (int64, error) {
	user, err := s.userRepo.FindByExternalID(ctx, externalID)
	if err != nil {
		return 0, ErrUserNotFound
	}
	return user.ID, nil
}

// ensureDeviceTokenOwnership verifies that externalID resolves to the owner
// of tokenID.
//
// The method intentionally compares internal IDs after resolving the verified
// external ID, so a client never controls either side of the authorization
// decision. It returns the relevant not-found or forbidden sentinel and is
// safe for concurrent use.
func (s *UserService) ensureDeviceTokenOwnership(ctx context.Context, externalID string, tokenID int64) error {
	userID, err := s.userIDByExternalID(ctx, externalID)
	if err != nil {
		return err
	}
	token, err := s.deviceRepo.FindByID(ctx, tokenID)
	if err != nil {
		return ErrDeviceTokenNotFound
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
// ErrUserNotFound rather than returning an empty list. The check is not
// atomic with the segment fetch, but the race is harmless — a user
// deleted between the two reads produces an empty list.
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
// Returns ErrUserNotFound if the user does not exist, or a wrapped
// "list segments by user: " error for a repository failure. The latter
// is not a sentinel, so the handler serves it through its default 500
// branch.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *UserService) GetUserSegments(ctx context.Context, userID int64) ([]UserSegment, error) {
	_, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, ErrUserNotFound
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
// # Pagination is over delivery rows, not notifications
//
// The underlying repository call ListByUser returns rows from
// notification_deliveries — one row per (notification, device) pair — not
// one row per notification. A user with three active devices therefore
// accumulates three rows for every notification received, and the total
// reported by the paginated response is a *delivery* count, not a
// notification count.
//
// The domain type UserNotification and the wire DTO
// UserNotificationHistoryItem both present these as "notifications in the
// user's history", which is misleading: a user who has received one
// notification on three devices will see three history entries. A client
// that displays "X notifications received" from the response's Total will
// over-count by the user's device count.
//
// Fixing the mismatch requires either a repository query that aggregates
// to one row per notification or a DTO that explicitly reports delivery
// counts. Neither exists today.
//
// # Existence check
//
// The method performs a FindByID lookup on the user before fetching the
// history, so a call for a non-existent user is rejected with
// ErrUserNotFound rather than returning an empty list.
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
// Returns ErrUserNotFound if the user does not exist. Returns a wrapped
// "list user notifications: " or "count user notifications: " error for
// repository failures; neither is a sentinel.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *UserService) GetUserNotifications(ctx context.Context, userID int64, page, limit int32) ([]UserNotification, int64, error) {
	_, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, 0, ErrUserNotFound
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
