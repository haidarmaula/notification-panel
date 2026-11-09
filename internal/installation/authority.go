package installation

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/cryptokey"
	"hello/internal/database/sqlc"
	"hello/internal/token"
)

var (
	ErrMobileCredential = errors.New("invalid mobile credential")
	ErrMobileOrigin     = errors.New("mobile origin verification required")
)

// MobileIdentityStore resolves projects and trusted subject mappings to active users.
type MobileIdentityStore interface {
	GetApplicationByPublicID(context.Context, pgtype.UUID) (sqlc.Application, error)
	ResolveMobileIdentity(context.Context, sqlc.ResolveMobileIdentityParams) (int64, error)
}

// OriginContext binds enrollment evidence to a project, identity and exact mobile JWT.
type OriginContext struct {
	ProjectID        pgtype.UUID
	Namespace        string
	Subject          string
	CredentialDigest [sha256.Size]byte
}

// OriginVerifier validates deployment-specific evidence for the exact project
// and authenticated identity. Its implementation owns challenge/replay checks.
type OriginVerifier interface {
	VerifyOrigin(context.Context, OriginContext, string) error
}

// MobilePolicy pins a project's JWT trust, identity namespace and enrollment origin verifier.
type MobilePolicy struct {
	Issuer          string
	Audience        string
	Purpose         string
	Namespace       string
	VerificationKey ed25519.PublicKey
	Origin          OriginVerifier
}

// MobileAuthority authenticates project-scoped mobile identities against current database state.
type MobileAuthority struct {
	store    MobileIdentityStore
	policies map[pgtype.UUID]MobilePolicy
}

func validIdentityText(value string) bool {
	return value != "" && len(value) <= 255 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

// NewMobileAuthority validates policies and copies the policy map and verification keys.
// Each configured project must have a distinct issuer/audience pair and an origin verifier.
func NewMobileAuthority(store MobileIdentityStore, policies map[pgtype.UUID]MobilePolicy) (*MobileAuthority, error) {
	if store == nil {
		return nil, ErrConfiguration
	}
	result := &MobileAuthority{store: store, policies: make(map[pgtype.UUID]MobilePolicy, len(policies))}
	pairs := make(map[[2]string]struct{}, len(policies))
	for id, policy := range policies {
		pair := [2]string{policy.Issuer, policy.Audience}
		_, duplicate := pairs[pair]
		if !id.Valid || duplicate || !validIdentityText(policy.Issuer) || !validIdentityText(policy.Audience) ||
			!validIdentityText(policy.Purpose) || !validIdentityText(policy.Namespace) ||
			!cryptokey.StrongEd25519PublicKey(policy.VerificationKey) || policy.Origin == nil {
			return nil, ErrConfiguration
		}
		pairs[pair] = struct{}{}
		policy.VerificationKey = append(ed25519.PublicKey(nil), policy.VerificationKey...)
		result.policies[id] = policy
	}
	return result, nil
}

type mobileClaims struct {
	Purpose string `json:"purpose"`
	jwt.RegisteredClaims
}

// Authenticate verifies the project JWT and resolves its subject to an active user.
// It does not verify app origin and cannot establish enrollment authority by itself.
func (a *MobileAuthority) Authenticate(ctx context.Context, project pgtype.UUID, credential string) (Principal, error) {
	session, err := a.AuthenticateSession(ctx, project, credential)
	return session.Principal(), err
}

// AuthenticateEnrollment also verifies origin evidence bound to the authenticated JWT.
// The configured verifier owns replay protection; consumed evidence may need replacement
// if the subsequent enrollment fails.
func (a *MobileAuthority) AuthenticateEnrollment(ctx context.Context, project pgtype.UUID, credential, evidence string) (Principal, error) {
	session, err := a.AuthenticateEnrollmentSession(ctx, project, credential, evidence)
	return session.Principal(), err
}

func (a *MobileAuthority) authenticate(ctx context.Context, project pgtype.UUID, credential string, evidence *string) (MobileSession, error) {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	policy, ok := a.policies[project]
	if !ok || len(credential) == 0 || len(credential) > 8192 {
		return MobileSession{}, ErrMobileCredential
	}
	var claims mobileClaims
	parsed, err := jwt.ParseWithClaims(credential, &claims, func(_ *jwt.Token) (any, error) {
		return policy.VerificationKey, nil
	}, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(policy.Issuer), jwt.WithAudience(policy.Audience), jwt.WithExpirationRequired(), jwt.WithStrictDecoding())
	if err != nil || parsed == nil || !parsed.Valid || !token.HasUniqueJWTFields(credential) ||
		len(claims.Audience) != 1 || claims.Purpose != policy.Purpose || !validIdentityText(claims.Subject) {
		return MobileSession{}, ErrMobileCredential
	}
	app, err := a.store.GetApplicationByPublicID(ctx, project)
	if errors.Is(err, pgx.ErrNoRows) {
		return MobileSession{}, ErrMobileCredential
	}
	if err != nil {
		return MobileSession{}, err
	}
	if app.ID <= 0 || app.PublicID != project {
		return MobileSession{}, ErrMobileCredential
	}
	if app.Status != "ACTIVE" {
		return MobileSession{}, ErrProjectInactive
	}
	userID, err := a.store.ResolveMobileIdentity(ctx, sqlc.ResolveMobileIdentityParams{ApplicationID: app.ID, IdentityNamespace: policy.Namespace, Subject: claims.Subject})
	if errors.Is(err, pgx.ErrNoRows) {
		return MobileSession{}, ErrMobileCredential
	}
	if err != nil {
		return MobileSession{}, err
	}
	if userID <= 0 {
		return MobileSession{}, ErrMobileCredential
	}
	if evidence != nil {
		if len(*evidence) == 0 || len(*evidence) > 16384 {
			return MobileSession{}, ErrMobileOrigin
		}
		binding := OriginContext{ProjectID: project, Namespace: policy.Namespace, Subject: claims.Subject, CredentialDigest: sha256.Sum256([]byte(credential))}
		if err := policy.Origin.VerifyOrigin(ctx, binding, *evidence); err != nil {
			return MobileSession{}, err
		}
	}
	return MobileSession{actor: Principal{ApplicationID: app.ID, UserID: userID}, project: project, namespace: policy.Namespace,
		subject: claims.Subject, expires: claims.ExpiresAt.Time, enrollmentVerified: evidence != nil}, nil
}
