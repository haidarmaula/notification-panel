package installation

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

const originPurpose = "mobile_installation_enrollment"
const maxOriginLifetime = 2 * time.Minute

type OriginUseStore interface {
	ConsumeMobileOrigin(context.Context, sqlc.ConsumeMobileOriginParams) (int64, error)
}

// SignedOrigin verifies assertions from a trusted attestation broker. The
// broker holds the signing key and verifies platform evidence before signing.
type SignedOrigin struct {
	store    OriginUseStore
	issuer   string
	audience string
	key      ed25519.PublicKey
}

func NewSignedOrigin(store OriginUseStore, issuer, audience string, key ed25519.PublicKey) (*SignedOrigin, error) {
	if store == nil || !validIdentityText(issuer) || !validIdentityText(audience) || len(key) != ed25519.PublicKeySize {
		return nil, ErrConfiguration
	}
	return &SignedOrigin{store: store, issuer: issuer, audience: audience, key: append(ed25519.PublicKey(nil), key...)}, nil
}

type originClaims struct {
	ProjectID        string `json:"app_id"`
	Namespace        string `json:"namespace"`
	CredentialDigest string `json:"credential_digest"`
	Purpose          string `json:"purpose"`
	jwt.RegisteredClaims
}

func (v *SignedOrigin) VerifyOrigin(ctx context.Context, binding OriginContext, evidence string) error {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if !binding.ProjectID.Valid || len(evidence) == 0 || len(evidence) > 16384 {
		return ErrMobileOrigin
	}
	var claims originClaims
	parsed, err := jwt.ParseWithClaims(evidence, &claims, func(_ *jwt.Token) (any, error) {
		return v.key, nil
	}, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(v.issuer), jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithStrictDecoding())
	if err != nil || !parsed.Valid || len(claims.Audience) != 1 || claims.IssuedAt == nil ||
		claims.Purpose != originPurpose || claims.ProjectID != binding.ProjectID.String() ||
		claims.Namespace != binding.Namespace || claims.Subject != binding.Subject || !validIdentityText(claims.ID) {
		return ErrMobileOrigin
	}
	lifetime := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time)
	if lifetime <= 0 || lifetime > maxOriginLifetime {
		return ErrMobileOrigin
	}
	expected := base64.RawURLEncoding.EncodeToString(binding.CredentialDigest[:])
	if subtle.ConstantTimeCompare([]byte(claims.CredentialDigest), []byte(expected)) != 1 {
		return ErrMobileOrigin
	}
	nonce := sha256.Sum256([]byte(claims.ID))
	n, err := v.store.ConsumeMobileOrigin(ctx, sqlc.ConsumeMobileOriginParams{
		ApplicationPublicID: binding.ProjectID, Issuer: v.issuer, NonceDigest: nonce[:],
		ExpiresAt: pgtype.Timestamptz{Time: claims.ExpiresAt.Time, Valid: true},
	})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrMobileOrigin
	}
	return nil
}
