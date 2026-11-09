// Package token signs and parses the two staff JWT classes. The API separately
// checks each authenticated session against current database authority.
package token

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"hello/internal/jsonobject"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 24 * time.Hour
)

// ErrInvalidToken is shared by all verification failures.
var ErrInvalidToken = errors.New("invalid token")

// AccessClaims supplies staff identity and session binding. Role and email are
// descriptive. Route and service authorization resolve current database grants.
type AccessClaims struct {
	StaffID   int64  `json:"user_id"`
	RoleName  string `json:"role_name"`
	Email     string `json:"email"`
	Type      string `json:"type"`
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

// RefreshClaims binds one single-use refresh identity to its session family.
type RefreshClaims struct {
	StaffID   int64  `json:"user_id"`
	RoleName  string `json:"role_name"`
	Type      string `json:"type"`
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

// Family fixes the absolute expiry at login. Rotation never extends it.
type Family struct {
	ID        pgtype.UUID
	ExpiresAt time.Time
}

// NewFamily creates an unpredictable identifier and second-precision JWT expiry.
func NewFamily() Family {
	id := pgtype.UUID{Valid: true}
	rand.Read(id.Bytes[:])
	id.Bytes[6] = id.Bytes[6]&0x0f | 0x40
	id.Bytes[8] = id.Bytes[8]&0x3f | 0x80
	return Family{ID: id, ExpiresAt: time.Now().UTC().Truncate(time.Second).Add(refreshTTL)}
}

// Pair contains newly signed credentials and the only refresh material persisted.
type Pair struct {
	AccessToken   string
	RefreshToken  string
	RefreshDigest [32]byte
}

// TokenManager uses separate immutable keys for staff access and rotation.
type TokenManager struct {
	issuer          string
	access, refresh purposeKeys
}

// NewTokenManager retains the local development constructor. Deployments use ParsePolicy.
func NewTokenManager(accessSecret, refreshSecret string) *TokenManager {
	return &TokenManager{
		issuer:  "hello-local-staff",
		access:  localPurpose("hello-local-api", "dev-access", accessSecret),
		refresh: localPurpose("hello-local-refresh", "dev-refresh", refreshSecret),
	}
}

func registered(staffID int64, issuer, audience string, now, expiry time.Time) jwt.RegisteredClaims {
	var nonce [32]byte
	rand.Read(nonce[:])
	return jwt.RegisteredClaims{
		Issuer: issuer, Audience: jwt.ClaimStrings{audience},
		Subject: strconv.FormatInt(staffID, 10), ID: base64.RawURLEncoding.EncodeToString(nonce[:]),
		ExpiresAt: jwt.NewNumericDate(expiry), IssuedAt: jwt.NewNumericDate(now),
	}
}

// Issue signs a candidate pair. Callers release it only after the corresponding
// family insertion or compare-and-rotate operation commits.
func (t *TokenManager) Issue(staffID int64, role, email string, f Family) (Pair, error) {
	now := time.Now().UTC().Truncate(time.Second)
	if staffID < 1 || !f.ID.Valid || f.ID.Bytes == [16]byte{} ||
		!f.ExpiresAt.After(now) || f.ExpiresAt.After(now.Add(refreshTTL)) {
		return Pair{}, ErrInvalidToken
	}
	accessUntil := now.Add(accessTTL)
	if f.ExpiresAt.Before(accessUntil) {
		accessUntil = f.ExpiresAt
	}
	sid := f.ID.String()
	access := AccessClaims{StaffID: staffID, RoleName: role, Email: email, SessionID: sid, Type: "access",
		RegisteredClaims: registered(staffID, t.issuer, t.access.audience, now, accessUntil)}
	refresh := RefreshClaims{StaffID: staffID, RoleName: role, SessionID: sid, Type: "refresh",
		RegisteredClaims: registered(staffID, t.issuer, t.refresh.audience, now, f.ExpiresAt)}
	signedAccess, err := t.access.sign(access)
	if err != nil {
		return Pair{}, err
	}
	signedRefresh, err := t.refresh.sign(refresh)
	if err != nil {
		return Pair{}, err
	}
	return Pair{AccessToken: signedAccess, RefreshToken: signedRefresh, RefreshDigest: sha256.Sum256([]byte(refresh.ID))}, nil
}

func validSession(staffID int64, sid string, claims jwt.RegisteredClaims, maxTTL time.Duration) bool {
	var family pgtype.UUID
	if staffID <= 0 || claims.Subject != strconv.FormatInt(staffID, 10) || family.Scan(sid) != nil || !family.Valid ||
		family.Bytes == [16]byte{} || family.String() != sid || claims.IssuedAt == nil || claims.ExpiresAt == nil ||
		!claims.ExpiresAt.After(claims.IssuedAt.Time) || claims.ExpiresAt.Sub(claims.IssuedAt.Time) > maxTTL ||
		len(claims.Audience) != 1 || len(claims.ID) != 43 {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(claims.ID)
	return err == nil && len(raw) == 32
}

func (t *TokenManager) parse(raw string, claims jwt.Claims, keys purposeKeys) error {
	if len(raw) == 0 || len(raw) > 8192 {
		return ErrInvalidToken
	}
	_, err := jwt.ParseWithClaims(raw, claims, keys.keyFor,
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithIssuedAt(),
		jwt.WithStrictDecoding(), jwt.WithIssuer(t.issuer), jwt.WithAudience(keys.audience))
	if err != nil || !HasUniqueJWTFields(raw) {
		return ErrInvalidToken
	}
	return nil
}

// HasUniqueJWTFields requires unique lowercase JSON members in the compact JWT
// header and payload. It does not verify the token's signature or claims.
func HasUniqueJWTFields(raw string) bool {
	if len(raw) == 0 || len(raw) > 16384 {
		return false
	}
	header, rest, _ := strings.Cut(raw, ".")
	payload, signature, ok := strings.Cut(rest, ".")
	if !ok || signature == "" || strings.Contains(signature, ".") {
		return false
	}
	for _, segment := range []string{header, payload} {
		data, err := base64.RawURLEncoding.Strict().DecodeString(segment)
		data = bytes.TrimLeft(data, " \t\r\n")
		if err != nil || len(data) == 0 || data[0] != '{' || !jsonobject.UniqueLowercaseFields(data) {
			return false
		}
	}
	return true
}

// ParseAccessToken enforces a signed staff identity, an expiry and family and
// token identifiers before the caller touches the database.
func (t *TokenManager) ParseAccessToken(raw string) (*AccessClaims, error) {
	var c AccessClaims
	if t.parse(raw, &c, t.access) != nil || c.Type != "access" || !validSession(c.StaffID, c.SessionID, c.RegisteredClaims, accessTTL) {
		return nil, ErrInvalidToken
	}
	return &c, nil
}

// ParseRefreshToken validates a single-use credential under the refresh key.
func (t *TokenManager) ParseRefreshToken(raw string) (*RefreshClaims, error) {
	var c RefreshClaims
	if t.parse(raw, &c, t.refresh) != nil || c.Type != "refresh" || !validSession(c.StaffID, c.SessionID, c.RegisteredClaims, refreshTTL) {
		return nil, ErrInvalidToken
	}
	return &c, nil
}
