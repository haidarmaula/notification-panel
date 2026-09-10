package auth

import (
	"context"
	"errors"

	"hello/internal/database/repository"
	"hello/internal/token"

	"golang.org/x/crypto/bcrypt"
)

// Authentication errors.
//
// These sentinels are returned by AuthService methods and translated into
// HTTP status codes by AuthHandler. They are declared here — rather than in
// errors.go as some packages in this repository do — because every one of
// them is produced and consumed within this file.
//
// # Handler-mapping coverage
//
// Not every error is currently mapped by every handler. See the individual
// variable docs for the mismatch on the refresh path.
var (
	// ErrInvalidCredentials is returned by Login when the supplied email
	// does not match a staff user, when the matched user is inactive, or
	// when the supplied password does not match the stored bcrypt hash. It
	// is also returned by RefreshToken when the staff row referenced by a
	// valid refresh token cannot be found or has been deactivated.
	//
	// The single sentinel covers all of those cases deliberately: an
	// attacker probing the login endpoint cannot distinguish "no such user"
	// from "wrong password" from "account disabled" by inspecting the
	// response. The error message — "invalid email or password" — reflects
	// that decision.
	//
	// # Handler coverage on the refresh path
	//
	// The AuthHandler.Login method maps ErrInvalidCredentials to 401. The
	// AuthHandler.RefreshToken method does not: it only recognises
	// token.ErrInvalidToken, so any ErrInvalidCredentials returned from the
	// refresh path currently falls through to the handler's default branch
	// and is served as a 500 Internal Server Error. A client whose refresh
	// token is valid but whose account has been deactivated since the token
	// was issued therefore receives a 500 rather than a 401. This is a
	// defect in the handler's error-mapping switch, not in this sentinel's
	// definition; the fix is to add ErrInvalidCredentials to that switch.
	ErrInvalidCredentials = errors.New("invalid email or password")

	// ErrEmailAlreadyRegistered is declared here for historical reasons but
	// is not returned by any method in this package. It is consumed by the
	// staff feature service (internal/features/staff) during staff-user
	// creation, where an attempt to insert a duplicate email is surfaced
	// with this sentinel. Any caller that encounters it should treat it as
	// a staff-creation conflict, not as an authentication failure.
	ErrEmailAlreadyRegistered = errors.New("email already registered")

	// ErrInvalidStaffRoleID is returned when a staff user references a role
	// ID that does not exist in the roles table. In practice this indicates
	// either data corruption — a staff row whose role_id was never
	// constrained by a foreign key, or was orphaned by a role delete — or,
	// on the refresh path, a bug: see RefreshToken for the current
	// misidentification of which ID is passed to the role lookup.
	//
	// The handler maps this error through the default branch and serves a
	// generic 500; the underlying cause is not exposed to the client.
	ErrInvalidStaffRoleID = errors.New("invalid staff role id")
)

// LoginResult holds the tokens returned after a successful login.
//
// It is the service-layer counterpart of the wire-level LoginResponse DTO.
// The two are deliberately separate so that the service can evolve its
// internal shape — for example, adding a `StaffID` field for logging or an
// `ExpiresAt` timestamp for downstream caching — without changing the JSON
// contract returned to clients. The handler is responsible for translating
// one into the other.
type LoginResult struct {
	// AccessToken is the short-lived bearer token minted by the token
	// manager for the authenticated staff user.
	AccessToken string

	// RefreshToken is the long-lived token that can be redeemed at the
	// refresh endpoint to obtain a new access token. It is not rotated by
	// this endpoint.
	RefreshToken string
}

// AuthService handles staff authentication and token management.
//
// It is the business-logic layer of the auth feature: it reads staff and
// role rows through the repository layer, verifies bcrypt password hashes,
// and delegates token issuance and verification to the token package. It
// performs no HTTP handling and no persistence writes of its own — the
// login flow is entirely read-only.
//
// # Dependencies
//
// The service is constructed with three collaborators:
//
//   - staffRepo, used to look up a staff user by email (login) and by ID
//     (refresh).
//   - roleRepo, used to resolve the staff user's role name for inclusion in
//     the access token's role_name claim.
//   - tokenManager, used to mint the access and refresh tokens, and to
//     verify the refresh token on redemption.
//
// All three are stored by reference and must be non-nil. The service does
// not own them and does not close or dispose of them.
//
// # Concurrency
//
// AuthService is stateless after construction and safe for concurrent use.
// It holds no per-request state, performs no locking, and shares no mutable
// data between invocations. The dependencies it holds — repositories built
// on pgxpool and a token manager built on immutable byte slices — are
// themselves safe for concurrent use.
type AuthService struct {
	staffRepo    *repository.StaffUserRepository
	roleRepo     *repository.RoleRepository
	tokenManager *token.TokenManager
}

// NewAuthService creates a new AuthService instance with the required dependencies.
//
// All three parameters must be non-nil. The constructor does not validate
// them; a nil repository or token manager will panic on the first call to
// Login or RefreshToken rather than at construction. In practice the
// dependencies are wired by NewAuthModule, which constructs each of them
// from the shared sqlc.Queries and token manager.
//
// The returned service is cheap to construct and holds no external
// resources. There is no Close method — none is needed, because the service
// does not own a connection pool or a background goroutine.
func NewAuthService(
	staffRepo *repository.StaffUserRepository,
	roleRepo *repository.RoleRepository,
	tokenManager *token.TokenManager,
) *AuthService {
	return &AuthService{
		staffRepo:    staffRepo,
		roleRepo:     roleRepo,
		tokenManager: tokenManager,
	}
}

// Login authenticates a staff user by email and password.
// Returns access and refresh tokens on success, or ErrInvalidCredentials if
// the credentials are incorrect or the account is inactive.
//
// # Flow
//
// The method performs four steps in order:
//
//  1. Looks up the staff user by email via staffRepo.FindByEmail. Any error
//     from this call — including the repository's pgx.ErrNoRows for a
//     missing user — is collapsed into ErrInvalidCredentials. The
//     underlying error is discarded.
//  2. Checks the row's IsActive flag. A deactivated staff user is rejected
//     with the same ErrInvalidCredentials, so the caller cannot distinguish
//     "no such user" from "disabled user" from the response.
//  3. Compares the supplied plaintext password against the stored bcrypt
//     hash via bcrypt.CompareHashAndPassword. A mismatch is
//     ErrInvalidCredentials. The plaintext password is not logged, not
//     retained, and not passed anywhere outside this function.
//  4. Resolves the user's role by roleRepo.FindByID(staff.RoleID) and mints
//     an access token (carrying staff ID, role name, and email) and a
//     refresh token (carrying staff ID and role name). Any error from the
//     role lookup or the token mint surfaces to the caller unchanged:
//     ErrInvalidStaffRoleID for a missing role row, or a token-package
//     error for a signing failure. The handler maps these through its
//     default branch to a generic 500.
//
// # Input validation
//
// The method does not validate that email or password are non-empty. An
// empty email will fail the repository lookup and produce
// ErrInvalidCredentials; an empty password will fail bcrypt comparison and
// produce the same. Callers that wish to reject empty inputs at the
// boundary must do so at the handler layer.
//
// # Errors
//
//   - ErrInvalidCredentials for a missing, inactive, or wrong-password user.
//   - ErrInvalidStaffRoleID if the staff row's role_id does not resolve to a
//     role row.
//   - Any error returned by the token manager's Generate* methods. These are
//     unexpected and indicate a signing configuration problem.
//
// The method does not wrap its errors; callers use errors.Is against the
// sentinels.
//
// # Concurrency
//
// Safe for concurrent use. The method allocates per-call state and shares
// nothing mutable with other invocations.
func (s *AuthService) Login(
	ctx context.Context,
	email string,
	password string,
) (*LoginResult, error) {
	staff, err := s.staffRepo.FindByEmail(ctx, email)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	if !staff.IsActive {
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword(
		[]byte(staff.PasswordHash),
		[]byte(password),
	); err != nil {
		return nil, ErrInvalidCredentials
	}

	role, err := s.roleRepo.FindByID(ctx, staff.RoleID)
	if err != nil {
		return nil, ErrInvalidStaffRoleID
	}

	accessToken, err := s.tokenManager.GenerateAccessToken(
		staff.ID,
		role.Name,
		staff.Email,
	)
	if err != nil {
		return nil, err
	}

	refreshToken, err := s.tokenManager.GenerateRefreshToken(staff.ID, role.Name)
	if err != nil {
		return nil, err
	}

	return &LoginResult{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

// RefreshToken validates a refresh token and issues a new access token.
// Returns ErrInvalidCredentials if the token is invalid, expired, or the
// associated staff account is inactive.
//
// # Flow
//
// The method performs four steps in order:
//
//  1. Parses and verifies the refresh token via
//     tokenManager.ParseRefreshToken. Any failure — malformed string, wrong
//     signature, expired, wrong token class — is collapsed into
//     token.ErrInvalidToken (not ErrInvalidCredentials). This is the one
//     error the AuthHandler.RefreshToken method recognises; it maps to 401.
//  2. Re-reads the staff user from the database by claims.StaffID. This is
//     deliberate: it ensures that a role change or account deactivation
//     that occurred after the refresh token was issued is reflected in the
//     new access token. A missing staff row is ErrInvalidCredentials; see
//     the "Known defects" section below for how this is handled downstream.
//  3. Checks the row's IsActive flag. A deactivated staff user is
//     ErrInvalidCredentials, again with the same downstream caveat.
//  4. Resolves the user's role and mints a new access token. The refresh
//     token itself is not rotated and is not returned — the caller reuses
//     the same refresh token until it expires.
//
// # Known defects
//
// Two defects are present in the current implementation. Both are documented
// here rather than corrected, because correcting them is out of scope for a
// documentation pass.
//
//  1. The role lookup passes the wrong ID. The call to
//     roleRepo.FindByID uses staff.ID (the staff user's own primary key)
//     instead of staff.RoleID (the foreign key that identifies the role).
//     Compare with Login, which uses staff.RoleID correctly. In practice
//     this means the role resolution almost always fails — surfacing as
//     ErrInvalidStaffRoleID and, via the handler, as a 500 — or, when a
//     role happens to share the numeric ID, succeeds while returning a
//     role unrelated to the staff user. The latter case is a
//     security-relevant correctness bug: the minted access token would
//     carry a role_name claim that does not reflect the staff user's
//     actual role, and downstream authorization decisions based on that
//     claim would be wrong. The fix is to pass staff.RoleID.
//
//  2. ErrInvalidCredentials is not mapped to 401 by the refresh handler.
//     AuthHandler.RefreshToken only recognises token.ErrInvalidToken. Any
//     ErrInvalidCredentials returned from steps 2 or 3 above therefore
//     falls through to the handler's default branch and is served as a 500
//     Internal Server Error. A client whose refresh token is valid but
//     whose account has since been deactivated receives a 500 rather than
//     a 401, which is both incorrect and misleading for retry logic on the
//     client. The fix is to extend the handler's switch to recognise
//     ErrInvalidCredentials.
//
// # Response coupling
//
// The method returns *RefreshTokenResponse directly — the same type the
// handler serves to the client — rather than a service-layer result struct
// analogous to LoginResult. This couples the service layer to the wire DTO:
// changing the shape of RefreshTokenResponse changes both the service's
// contract and the endpoint's response in a single step. It is a divergence
// from the pattern used by Login, where a LoginResult mediates between the
// two layers. A future refactor to introduce a RefreshResult is safe but is
// not required for correctness today.
//
// # Errors
//
//   - token.ErrInvalidToken when the refresh token fails verification for
//     any reason.
//   - ErrInvalidCredentials when the staff row referenced by the token does
//     not exist, or exists but is inactive. See defect 2 above for how this
//     is (mis)handled by the current handler.
//   - ErrInvalidStaffRoleID when the role lookup fails. See defect 1 above.
//   - Any error returned by GenerateAccessToken. This is unexpected.
//
// # Concurrency
//
// Safe for concurrent use. The method allocates per-call state and shares
// nothing mutable with other invocations.
func (s *AuthService) RefreshToken(
	ctx context.Context,
	refreshToken string,
) (*RefreshTokenResponse, error) {
	claims, err := s.tokenManager.ParseRefreshToken(refreshToken)
	if err != nil {
		return nil, token.ErrInvalidToken
	}

	staff, err := s.staffRepo.FindByID(ctx, claims.StaffID)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	if !staff.IsActive {
		return nil, ErrInvalidCredentials
	}

	role, err := s.roleRepo.FindByID(ctx, staff.ID)
	if err != nil {
		return nil, ErrInvalidStaffRoleID
	}

	accessToken, err := s.tokenManager.GenerateAccessToken(
		staff.ID,
		role.Name,
		staff.Email,
	)
	if err != nil {
		return nil, err
	}

	return &RefreshTokenResponse{
		AccessToken: accessToken,
	}, nil
}
