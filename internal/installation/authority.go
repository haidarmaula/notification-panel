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
	"hello/internal/database/sqlc"
)

var (
	ErrMobileCredential = errors.New("invalid mobile credential")
	ErrMobileOrigin     = errors.New("mobile origin verification required")
)

type MobileIdentityStore interface {
	GetApplicationByPublicID(context.Context, pgtype.UUID) (sqlc.Application, error)
	ResolveMobileIdentity(context.Context, sqlc.ResolveMobileIdentityParams) (int64, error)
}

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

type MobilePolicy struct {
	Issuer          string
	Audience        string
	Purpose         string
	Namespace       string
	VerificationKey ed25519.PublicKey
	Origin          OriginVerifier
}

type MobileAuthority struct {
	store    MobileIdentityStore
	policies map[pgtype.UUID]MobilePolicy
}

func validIdentityText(value string) bool {
	return value != "" && len(value) <= 255 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

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
			len(policy.VerificationKey) != ed25519.PublicKeySize || policy.Origin == nil {
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

func (a *MobileAuthority) Authenticate(ctx context.Context, project pgtype.UUID, credential string) (Principal, error) {
	return a.authenticate(ctx, project, credential, nil)
}

func (a *MobileAuthority) AuthenticateEnrollment(ctx context.Context, project pgtype.UUID, credential, evidence string) (Principal, error) {
	return a.authenticate(ctx, project, credential, &evidence)
}

func (a *MobileAuthority) authenticate(ctx context.Context, project pgtype.UUID, credential string, evidence *string) (Principal, error) {
	policy, ok := a.policies[project]
	if !ok || len(credential) == 0 || len(credential) > 8192 {
		return Principal{}, ErrMobileCredential
	}
	var claims mobileClaims
	parsed, err := jwt.ParseWithClaims(credential, &claims, func(_ *jwt.Token) (any, error) {
		return policy.VerificationKey, nil
	}, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(policy.Issuer), jwt.WithAudience(policy.Audience), jwt.WithExpirationRequired(), jwt.WithStrictDecoding())
	if err != nil || !parsed.Valid || len(claims.Audience) != 1 || claims.Purpose != policy.Purpose || !validIdentityText(claims.Subject) {
		return Principal{}, ErrMobileCredential
	}
	app, err := a.store.GetApplicationByPublicID(ctx, project)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrMobileCredential
	}
	if err != nil {
		return Principal{}, err
	}
	if app.ID <= 0 || app.PublicID != project {
		return Principal{}, ErrMobileCredential
	}
	if app.Status != "ACTIVE" {
		return Principal{}, ErrProjectInactive
	}
	userID, err := a.store.ResolveMobileIdentity(ctx, sqlc.ResolveMobileIdentityParams{ApplicationID: app.ID, IdentityNamespace: policy.Namespace, Subject: claims.Subject})
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrMobileCredential
	}
	if err != nil {
		return Principal{}, err
	}
	if userID <= 0 {
		return Principal{}, ErrMobileCredential
	}
	if evidence != nil {
		if len(*evidence) == 0 || len(*evidence) > 16384 {
			return Principal{}, ErrMobileOrigin
		}
		binding := OriginContext{ProjectID: project, Namespace: policy.Namespace, Subject: claims.Subject, CredentialDigest: sha256.Sum256([]byte(credential))}
		if err := policy.Origin.VerifyOrigin(ctx, binding, *evidence); err != nil {
			return Principal{}, ErrMobileOrigin
		}
	}
	return Principal{ApplicationID: app.ID, UserID: userID}, nil
}
