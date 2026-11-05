package dispatch

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/cryptokey"
)

var ErrReplayCredential = errors.New("invalid operational replay credential")

const replayCredentialLifetime = 5 * time.Minute

// ReplayVerifier authenticates purpose-bound step-up evidence using a pinned key.
// It does not check current database authority or grant revocation; a successful
// verification alone must never authorize a replay mutation or publication.
type ReplayVerifier struct {
	issuer     string
	audience   string
	deployment string
	key        ed25519.PublicKey
}

// VerifiedReplay holds authenticated request provenance for transaction-time
// authority checks. Its zero value is invalid; only ReplayVerifier issues values.
type VerifiedReplay struct {
	request         ReplayRequest
	staffID         int64
	grantID         pgtype.UUID
	fingerprint     [32]byte
	expiresAt       time.Time
	authenticatedAt time.Time
	issuer          string
	deployment      string
}

// NewReplayVerifier pins a dedicated operational issuer, audience and deployment.
// The public key is copied. Key provisioning and trusted MFA issuance are external
// deployment gates; ordinary staff/mobile token issuers are not implicit fallbacks.
func NewReplayVerifier(issuer, audience, deployment string, key ed25519.PublicKey) (*ReplayVerifier, error) {
	if !validReplayText(issuer) || !validReplayText(audience) || !validReplayText(deployment) || !cryptokey.StrongEd25519PublicKey(key) {
		return nil, ErrReplayCredential
	}
	return &ReplayVerifier{issuer: issuer, audience: audience, deployment: deployment, key: slices.Clone(key)}, nil
}

type replayClaims struct {
	Purpose       string           `json:"purpose"`
	Deployment    string           `json:"deployment"`
	ApplicationID string           `json:"application_id"`
	RequestID     string           `json:"request_id"`
	GrantID       string           `json:"grant_id"`
	Fingerprint   string           `json:"selection_hash"`
	AuthTime      *jwt.NumericDate `json:"auth_time"`
	Methods       []string         `json:"amr"`
	jwt.RegisteredClaims
}

func replayClaimUUID(value string) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if len(value) != 36 || id.Scan(value) != nil || !validReplayUUID(id) || id.String() != value {
		return pgtype.UUID{}, false
	}
	return id, true
}

// Verify checks a signed, at-most-five-minute MFA assertion against the complete
// request. The credential needs at least five seconds remaining for a bounded
// attempt. Errors never contain the token, selected positions or decoder details.
// Current staff/app/grant authority must still be locked and checked per transaction.
func (v *ReplayVerifier) Verify(request ReplayRequest, credential string) (VerifiedReplay, error) {
	var invalid VerifiedReplay
	if v == nil || len(v.key) != ed25519.PublicKeySize || len(credential) == 0 || len(credential) > 8192 {
		return invalid, ErrReplayCredential
	}
	request, err := request.normalized()
	if err != nil {
		return invalid, err
	}
	var claims replayClaims
	parsed, err := jwt.ParseWithClaims(credential, &claims, func(_ *jwt.Token) (any, error) { return v.key, nil },
		jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(v.issuer), jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithStrictDecoding())
	if err != nil || parsed == nil || !parsed.Valid {
		return invalid, ErrReplayCredential
	}
	now := time.Now()
	if claims.Purpose != "notification_replay" || claims.Deployment != v.deployment || len(claims.Audience) != 1 ||
		!validReplayText(claims.ID) || claims.IssuedAt == nil || claims.AuthTime == nil || claims.ExpiresAt == nil ||
		!claims.ExpiresAt.After(claims.IssuedAt.Time) || claims.ExpiresAt.Sub(claims.IssuedAt.Time) > replayCredentialLifetime ||
		claims.ExpiresAt.Before(now.Add(5*time.Second)) || claims.AuthTime.After(claims.IssuedAt.Time) ||
		claims.AuthTime.After(now) || now.Sub(claims.AuthTime.Time) > replayCredentialLifetime-5*time.Second || !slices.Contains(claims.Methods, "mfa") {
		return invalid, ErrReplayCredential
	}
	app, ok := replayClaimUUID(claims.ApplicationID)
	if !ok || app != request.ApplicationID {
		return invalid, ErrReplayCredential
	}
	id, ok := replayClaimUUID(claims.RequestID)
	if !ok || id != request.ID {
		return invalid, ErrReplayCredential
	}
	grant, ok := replayClaimUUID(claims.GrantID)
	if !ok {
		return invalid, ErrReplayCredential
	}
	staffID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || staffID <= 0 || strconv.FormatInt(staffID, 10) != claims.Subject {
		return invalid, ErrReplayCredential
	}
	fingerprint, err := request.Fingerprint(staffID, grant)
	if err != nil {
		return invalid, err
	}
	claimed, err := base64.RawURLEncoding.Strict().DecodeString(claims.Fingerprint)
	if err != nil || len(claimed) != len(fingerprint) || subtle.ConstantTimeCompare(claimed, fingerprint[:]) != 1 {
		return invalid, ErrReplayCredential
	}
	expiresAt := claims.ExpiresAt.Time
	if mfaExpiry := claims.AuthTime.Add(replayCredentialLifetime); mfaExpiry.Before(expiresAt) {
		expiresAt = mfaExpiry
	}
	return VerifiedReplay{
		request: request, staffID: staffID, grantID: grant, fingerprint: fingerprint,
		expiresAt: expiresAt, authenticatedAt: claims.AuthTime.Time,
		issuer: v.issuer, deployment: v.deployment,
	}, nil
}
