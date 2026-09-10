package mobile

import (
	"context"
	"errors"
	"fmt"

	"hello/internal/database/repository"
	"hello/internal/database/sqlc"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Domain errors.
//
// These sentinels are returned by MobileService methods and translated into
// HTTP status codes by MobileHandler. They are the only values the handler
// recognises; every other error it receives falls through to its default
// 500 branch and — unlike the auth feature's handler — is served to the
// client with err.Error() verbatim.
var (
	// ErrInvalidJWT is returned by Sync when the Laravel-issued token in
	// the request body fails verification: malformed string, invalid
	// signature, expired, or signed with a non-HMAC algorithm. The handler
	// maps this to 401 Unauthorized.
	//
	// All failure modes collapse into this one sentinel; the client cannot
	// distinguish "expired" from "signature mismatch" from "wrong secret"
	// via the response.
	ErrInvalidJWT = errors.New("invalid or expired JWT")

	// ErrInvalidPlatform is returned by Sync when the request's platform
	// string is not one of the three accepted values: "ANDROID", "IOS", or
	// "WEB". The comparison is case-sensitive against those exact
	// uppercase literals; "android" or "iOS" are rejected.
	//
	// The handler maps this to 400 Bad Request. Note that Sync performs
	// this check *after* upserting the user — see Sync for the
	// partial-commit consequence.
	ErrInvalidPlatform = errors.New("invalid platform: must be ANDROID, IOS, or WEB")

	// ErrUserNotFound is declared here for historical reasons but is not
	// returned by any method in this package. Sync's user-upsert step
	// creates a missing user rather than failing, so there is no
	// "user not found" path in the current design. If a future revision
	// changes upsertUser to reject unknown users, this sentinel is the
	// value to return; the handler's default branch would serve it as a 500
	// until a corresponding case is added to its switch.
	ErrUserNotFound = errors.New("user not found")
)

// MobileService provides business logic for mobile app integration.
//
// It is the business-logic layer of the mobile feature. Given a
// Laravel-issued JWT and a device description, it verifies the token,
// reflects the user's current name and email into the local users table,
// and registers or refreshes the device's push token.
//
// # Dependencies
//
// The service is constructed with two repositories and one secret:
//
//   - userRepo, used to look up a user by external_id and to create or
//     update the local row.
//   - deviceRepo, used to look up a device by push token and to create or
//     update the corresponding device_tokens row.
//   - jwtSecret, the shared secret used to verify Laravel-issued tokens.
//     It is stored as a string and converted to []byte on each parse; see
//     the package comment for why this secret is distinct from the
//     staff-facing ACCESS_SECRET and REFRESH_SECRET.
//
// # Transactional behaviour
//
// The service does not open database transactions. Each repository call is
// its own unit of work, and the sequence of calls in Sync is not atomic —
// see Sync for the failure modes this produces.
//
// # Concurrency
//
// MobileService is stateless after construction and safe for concurrent
// use. It holds no per-request state and shares nothing mutable between
// invocations; the two repositories and the secret are read-only.
type MobileService struct {
	userRepo   *repository.UserRepository
	deviceRepo *repository.DeviceTokenRepository
	jwtSecret  string
}

// NewMobileService creates a new MobileService instance.
//
// userRepo and deviceRepo must be non-nil; they are stored directly and
// dereferenced on the first sync, so a nil value will panic at request time
// rather than at construction. In practice both are wired by
// NewMobileModule from the shared sqlc.Queries.
//
// jwtSecret is the shared secret used to verify Laravel-issued tokens; it
// is typically cfg.MobileJWTSecret, which the config loader guarantees is
// non-empty. An empty or incorrect secret will cause every sync attempt to
// fail with ErrInvalidJWT at the parse step — a fail-closed behaviour, but
// one that is worth confirming at startup rather than discovering from
// production traffic.
func NewMobileService(
	userRepo *repository.UserRepository,
	deviceRepo *repository.DeviceTokenRepository,
	jwtSecret string,
) *MobileService {
	return &MobileService{
		userRepo:   userRepo,
		deviceRepo: deviceRepo,
		jwtSecret:  jwtSecret,
	}
}

// SyncParams holds input for syncing user and device.
//
// It is the service-layer counterpart of the handler's SyncRequest. The
// optional device fields are pointers to match the DTO's
// omitted-versus-empty distinction, but the service does not currently
// preserve that distinction: a nil pointer is translated to a SQL NULL in
// the device update, which means an omitted field will overwrite any
// previously stored value with NULL. See registerDeviceToken for the exact
// behaviour.
//
// SyncParams is a plain data struct with no internal locking. It is
// constructed on the stack by the handler and passed by value; no
// synchronisation is required.
type SyncParams struct {
	// JWT is the Laravel-issued token. It must be non-empty; the handler
	// rejects an empty value before invoking the service, so a service
	// caller can assume it is present.
	JWT string

	// Platform is the client operating system. Accepted values are
	// "ANDROID", "IOS", and "WEB"; the comparison is case-sensitive.
	Platform string

	// PushToken is the provider-issued token that identifies the device.
	// It is the natural key for the device upsert.
	PushToken string

	// InstallationID, AppVersion, OSVersion, and DeviceModel are optional
	// device metadata. Nil means "not supplied by the client"; see
	// registerDeviceToken for how nil is treated on write.
	InstallationID *string
	AppVersion     *string
	OSVersion      *string
	DeviceModel    *string
}

// Sync synchronizes a user from JWT and registers their device token.
//
// # Flow
//
// The method performs four steps in the order shown:
//
//  1. Verify and parse the JWT via verifyJWT. Any failure — malformed
//     string, invalid signature, expired token, non-HMAC algorithm — is
//     collapsed into ErrInvalidJWT, and the method returns immediately. No
//     database work is performed in this case.
//  2. Upsert the user via upsertUser. The user is looked up by the JWT's
//     external_id; if found, the local name and email are refreshed from
//     the claims when they differ; if not found, a new user row is created
//     with status "ACTIVE". Errors from this step are wrapped with the
//     prefix "upsert user: " and returned as-is — they are not collapsed
//     into a sentinel, so the handler serves them via its default 500
//     branch.
//  3. Validate the platform string. The check is a case-sensitive equality
//     against the three accepted literals; a mismatch returns
//     ErrInvalidPlatform. See the partial-commit note below.
//  4. Register or refresh the device token via registerDeviceToken. Errors
//     from this step are wrapped with "register device token: " and
//     returned as-is.
//
// On success the method returns a MobileSyncResult carrying the internal
// user and device-token IDs. The response never includes the Laravel
// external_id.
//
// # Partial-commit caveat on invalid platform
//
// Platform validation is deliberately at step 3, after the user upsert at
// step 2. This means a request with a valid JWT but an unrecognised
// platform will create (or update) the user row and *then* fail with a 400.
// The mobile client sees a failed request; the database has been modified.
//
// The effect is confined by the upsert being idempotent — a retry with the
// same JWT and a corrected platform will simply find the existing user and
// proceed — so no data is corrupted and no duplicate user is created. But
// the response semantics are misleading: a 400 typically signals "nothing
// happened, fix the request and retry", and here that is only half true.
//
// Two revisions are possible and out of scope for a documentation pass:
// move the platform validation above the JWT parse (cheap, catches the
// error before any work), or accept the ordering and document the
// idempotency guarantee as sufficient. The current implementation takes
// the latter implicit position.
//
// # Error wrapping
//
// Steps 2 and 4 wrap their underlying errors with fmt.Errorf("...: %w").
// This preserves errors.Is and errors.As for callers, so the wrapping does
// not prevent sentinel matching — but because neither step currently
// returns a sentinel, the wrapping is the only error information the
// handler receives. Combined with the handler's default branch serving
// err.Error() verbatim, the wrapped message reaches the mobile client.
//
// # Concurrency
//
// Safe for concurrent use. The method allocates per-call state and shares
// nothing mutable with other invocations. However, because it does not
// open a transaction, two concurrent syncs with the same push token can
// race at the device-upsert step — see registerDeviceToken.
func (s *MobileService) Sync(ctx context.Context, params SyncParams) (*MobileSyncResult, error) {
	// 1. Verify and parse JWT
	claims, err := s.verifyJWT(params.JWT)
	if err != nil {
		return nil, ErrInvalidJWT
	}

	// 2. Upsert user
	user, err := s.upsertUser(ctx, claims)
	if err != nil {
		return nil, fmt.Errorf("upsert user: %w", err)
	}

	// 3. Validate platform
	if params.Platform != "ANDROID" && params.Platform != "IOS" && params.Platform != "WEB" {
		return nil, ErrInvalidPlatform
	}

	// 4. Register device token
	device, err := s.registerDeviceToken(ctx, user.ID, params)
	if err != nil {
		return nil, fmt.Errorf("register device token: %w", err)
	}

	return &MobileSyncResult{
		UserID:        user.ID,
		DeviceTokenID: device.ID,
	}, nil
}

// verifyJWT verifies and parses the JWT token.
//
// # Verification
//
// The token is verified against the service's jwtSecret using
// jwt.ParseWithClaims. The key function supplied to the parser additionally
// asserts that the token's signing method is HMAC-based, rejecting any
// token whose header declares a different algorithm family.
//
// This algorithm restriction is a hardening that internal/token's parsers
// do *not* currently implement. A future refactor of the token package
// should copy this pattern — restricting to the HMAC family (or, ideally,
// to HS256 specifically) closes the algorithm-agility gap noted in that
// package's documentation.
//
// # Expiry and registered claims
//
// Expiry (exp) is checked by the JWT library during parse, so an expired
// token produces a non-nil err and returns before the claims are
// inspected. No other registered claim is enforced: iss, aud, and sub are
// not compared against expected values, so any system sharing the mobile
// secret and producing a compatible claim shape will be accepted.
//
// # Safety on malformed input
//
// Unlike internal/token.ParseRefreshToken — which type-asserts the claims
// with the single-value form and would panic on a nil token — this function
// uses the two-value form (claims, ok := token.Claims.(*MobileClaims)).
// That form is safe: a failed assertion yields ok == false rather than
// panicking. Combined with the err check that precedes it, this function
// is safe for arbitrary attacker-controlled input.
//
// # Errors
//
// Returns a non-nil error on any parse or verification failure. The error
// is the underlying JWT library error (or a locally-constructed
// "unexpected signing method" error from the key function); it is not
// wrapped in ErrInvalidJWT. The caller — Sync — is responsible for
// collapsing it into ErrInvalidJWT before returning to the handler.
func (s *MobileService) verifyJWT(tokenString string) (*MobileClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &MobileClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.jwtSecret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*MobileClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}

// upsertUser creates or updates a user based on external_id from JWT.
//
// # Flow
//
// The user is first looked up by the JWT's external_id. If a row exists,
// the local name and email are compared against the claims; if either
// differs, an Update is issued writing both fields (not just the changed
// one) and the updated row is re-fetched. If the row does not exist, a new
// user is created with status "ACTIVE".
//
// # Field translation
//
// The claims' name and email are plain strings; the SQL layer accepts them
// as pgtype.Text{Valid: true} — i.e. a non-NULL empty string is stored if
// the claim is empty. An empty claim therefore overwrites an existing
// value with "", not with NULL. The comparison at the top of the update
// branch uses pgtype.Text.String directly, so a NULL name in the database
// compares equal to an empty claim and is left alone; a non-empty claim,
// however, will overwrite either a NULL or a non-empty existing value.
//
// # Status handling
//
// Status is not touched on the update path: the existing row's status is
// carried forward verbatim. On the create path, status is hardcoded to
// "ACTIVE" — a Laravel token cannot create a user in a non-active state.
// This is a deliberate asymmetry with the update path, where a user
// deactivated locally remains deactivated across syncs even if Laravel
// would consider them active.
//
// # Errors
//
// Returns the raw repository error for any failure of the lookup, update,
// create, or re-fetch calls. These are not sentinel-wrapped; Sync wraps
// the whole call with "upsert user: %w", producing a message the handler's
// default branch serves verbatim to the mobile client.
//
// # Concurrency
//
// Not transactionally isolated. Two concurrent syncs for the same
// external_id can both observe a missing user and both attempt Create; one
// will succeed and the other will fail with a unique-constraint violation
// on external_id. A mobile client calling sync twice at startup — which
// is a realistic pattern — can trigger this race.
func (s *MobileService) upsertUser(ctx context.Context, claims *MobileClaims) (*sqlc.User, error) {
	// Try to find by external_id
	existing, err := s.userRepo.FindByExternalID(ctx, claims.ExternalID)
	if err == nil {
		// User exists: update if any field changed
		if existing.Name.String != claims.Name || existing.Email.String != claims.Email {
			update := sqlc.UpdateUserParams{
				ID:     existing.ID,
				Name:   pgtype.Text{String: claims.Name, Valid: true},
				Email:  pgtype.Text{String: claims.Email, Valid: true},
				Status: existing.Status,
			}
			if err := s.userRepo.Update(ctx, update); err != nil {
				return nil, err
			}
			// Fetch updated user
			updated, err := s.userRepo.FindByID(ctx, existing.ID)
			if err != nil {
				return nil, err
			}
			return &updated, nil
		}
		return &existing, nil
	}

	// User does not exist: create new
	newUser, err := s.userRepo.Create(ctx, sqlc.CreateUserParams{
		ExternalID: claims.ExternalID,
		Name:       pgtype.Text{String: claims.Name, Valid: true},
		Email:      pgtype.Text{String: claims.Email, Valid: true},
		Status:     "ACTIVE",
	})
	if err != nil {
		return nil, err
	}
	return &newUser, nil
}

// registerDeviceToken registers or updates a device token for a user.
//
// # Flow
//
// The provider string is derived from the platform (ANDROID and WEB map to
// "FCM", IOS maps to "APNS"). The presence of the push token is then
// checked via ExistsByPushToken; if the token is already registered, the
// existing row is fetched, updated with the new metadata, and re-fetched;
// otherwise a new row is created.
//
// # Provider mapping
//
// The mapping is a three-case switch that assumes platform validation has
// already occurred in Sync. If a caller invokes this method directly with
// an unvalidated platform, provider will be the empty string and the row
// will be written with no provider attribution. Do not call this method
// directly without running the same platform check; it is unexported for
// this reason but should be treated as an internal invariant.
//
// # Optional fields become NULL overwrites
//
// The four optional device fields (installation_id, app_version, os_version,
// device_model) are translated from *string to pgtype.Text by a nil check.
// When the pointer is nil, the pgtype.Text remains at its zero value —
// {Valid: false} — which serialises to SQL NULL. Because the update uses
// UpdateDeviceTokenFull (an unconditional full overwrite), a mobile client
// that omits app_version on one sync will wipe any previously recorded
// app_version on that device row.
//
// This is a subtle behavioural consequence of the DTO's use of *string:
// the pointer type exists to distinguish "omitted" from "empty", but the
// service does not use that distinction — it treats both as NULL. A mobile
// client that conditionally sends optional fields will produce a device
// row whose optional columns oscillate between populated and NULL.
//
// The safe behaviour, if it is desired, is to only include non-nil fields
// in the update params and use a partial-update query; the current code
// uses UpdateDeviceTokenFull unconditionally. This is a design decision,
// not a bug per se, but it is not obvious from the method signature.
//
// # Device tokens are not transferred between users
//
// The update path preserves the existing row's user_id: UpdateDeviceTokenFull
// does not carry a user_id field, and the call passes only the token's
// metadata. Consequently, when a device that was previously registered to
// user A syncs under a JWT for user B, the update writes B's device
// metadata onto A's row. The sync succeeds and returns user B's internal
// ID together with the (still A-owned) device token ID — a misleading
// result, and a real correctness issue in the "device handed to another
// account" and "device wiped and re-registered under a different account"
// scenarios.
//
// A correct implementation would either overwrite user_id with the
// current user's ID (making the device's owner track the most recent
// sync), or treat the push token as owned by the user that first
// registered it and reject the mismatched sync. Neither is implemented
// here.
//
// # No transaction around the existence check
//
// The sequence ExistsByPushToken → FindByPushToken / UpdateFull / Create is
// not wrapped in a transaction. Two concurrent syncs carrying the same
// push token can both observe exists == false and both attempt Create; the
// second will fail with a unique-constraint violation on push_token. The
// mobile client's typical "sync on every app launch" pattern makes this a
// realistic race under concurrent cold starts.
//
// # Re-fetch on the update path
//
// After UpdateFull succeeds, the row is re-fetched via FindByID to obtain
// the post-update state (which may include database-generated columns or
// timestamp changes). The re-fetch adds a round trip but produces a
// complete result for the caller.
//
// # Errors
//
// Returns the raw repository error for any failure of the Exists, Find,
// Update, Create, or re-fetch calls. These are not sentinel-wrapped; Sync
// wraps the whole call with "register device token: %w".
func (s *MobileService) registerDeviceToken(
	ctx context.Context,
	userID int64,
	params SyncParams,
) (*sqlc.DeviceToken, error) {
	// Determine provider based on platform
	var provider string
	switch params.Platform {
	case "ANDROID":
		provider = "FCM"
	case "IOS":
		provider = "APNS"
	case "WEB":
		provider = "FCM"
	}

	// Prepare optional fields
	var installationID, appVersion, osVersion, deviceModel pgtype.Text
	if params.InstallationID != nil {
		installationID = pgtype.Text{String: *params.InstallationID, Valid: true}
	}
	if params.AppVersion != nil {
		appVersion = pgtype.Text{String: *params.AppVersion, Valid: true}
	}
	if params.OSVersion != nil {
		osVersion = pgtype.Text{String: *params.OSVersion, Valid: true}
	}
	if params.DeviceModel != nil {
		deviceModel = pgtype.Text{String: *params.DeviceModel, Valid: true}
	}

	// Check if token already exists
	exists, err := s.deviceRepo.ExistsByPushToken(ctx, params.PushToken)
	if err != nil {
		return nil, err
	}

	if exists {
		// Find and update existing token
		existingToken, err := s.deviceRepo.FindByPushToken(ctx, params.PushToken)
		if err != nil {
			return nil, err
		}

		update := sqlc.UpdateDeviceTokenFullParams{
			ID:             existingToken.ID,
			Platform:       params.Platform,
			InstallationID: installationID,
			PushToken:      params.PushToken,
			Provider:       provider,
			AppVersion:     appVersion,
			OsVersion:      osVersion,
			DeviceModel:    deviceModel,
		}
		if err := s.deviceRepo.UpdateFull(ctx, update); err != nil {
			return nil, err
		}

		// Fetch updated token
		updated, err := s.deviceRepo.FindByID(ctx, existingToken.ID)
		if err != nil {
			return nil, err
		}
		return &updated, nil
	}

	// Create new token
	newToken, err := s.deviceRepo.Create(ctx, sqlc.CreateDeviceTokenParams{
		UserID:         userID,
		Platform:       params.Platform,
		InstallationID: installationID,
		PushToken:      params.PushToken,
		Provider:       provider,
		AppVersion:     appVersion,
		OsVersion:      osVersion,
		DeviceModel:    deviceModel,
	})
	if err != nil {
		return nil, err
	}
	return &newToken, nil
}
