// Package receipt signs narrowly scoped payload-possession capabilities. A valid
// signature does not replace current mobile/installation database authority.
package receipt

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/cryptokey"
)

var (
	ErrConfiguration = errors.New("invalid receipt key configuration")
	ErrInvalid       = errors.New("invalid receipt capability")
	ErrExpired       = errors.New("receipt capability expired")
)

const (
	domain        = "hello/receipt-capability/v1\x00"
	payloadSize   = 96
	maxTokenSize  = 256
	maxExpiryUnix = 253402300799
	redacted      = "[REDACTED]"
)

// Scope is the exact committed delivery identity and capability expiry. UUIDs
// are network-order bytes. Expiry/key selection must be persisted before send;
// retries must not compute a new observation window or audience identity.
type Scope struct {
	ApplicationID  pgtype.UUID
	OriginEpoch    pgtype.UUID
	NotificationID int64
	DeliveryID     pgtype.UUID
	BindingID      int64
	InstallationID pgtype.UUID
	OwnerEpoch     int64
	ExpiresUnix    int64
}

func (s Scope) valid() bool {
	for _, id := range []pgtype.UUID{s.ApplicationID, s.OriginEpoch, s.DeliveryID, s.InstallationID} {
		if !id.Valid || id.Bytes == [16]byte{} {
			return false
		}
	}
	return s.NotificationID > 0 && s.BindingID > 0 && s.OwnerEpoch > 0 && s.ExpiresUnix > 0 && s.ExpiresUnix <= maxExpiryUnix
}
func (s Scope) payload() []byte {
	b := make([]byte, payloadSize)
	copy(b[0:16], s.ApplicationID.Bytes[:])
	copy(b[16:32], s.OriginEpoch.Bytes[:])
	binary.BigEndian.PutUint64(b[32:40], uint64(s.NotificationID))
	copy(b[40:56], s.DeliveryID.Bytes[:])
	binary.BigEndian.PutUint64(b[56:64], uint64(s.BindingID))
	copy(b[64:80], s.InstallationID.Bytes[:])
	binary.BigEndian.PutUint64(b[80:88], uint64(s.OwnerEpoch))
	binary.BigEndian.PutUint64(b[88:96], uint64(s.ExpiresUnix))
	return b
}

// Capability hides its wire value from formatting, slog and ordinary JSON.
// Possession is sensitive; only the transport should call WireValue.
type Capability struct {
	environment string
	scope       Scope
	wire        string
}

func (c Capability) String() string               { return redacted }
func (c Capability) GoString() string             { return redacted }
func (c Capability) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (c Capability) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

// WireValue reveals the capability for the mobile push payload only. Do not log,
// trace or expose it through admin APIs or arbitrary delivery listing endpoints.
func (c Capability) WireValue() string { return c.wire }

// Matches rejects zero capabilities and mismatched deployment/delivery scope.
func (c Capability) Matches(environment string, scope Scope) bool {
	return c.wire != "" && c.environment == environment && scope.valid() && c.scope == scope
}

// Signer retains dedicated private keys. Configuration is copied and immutable;
// there is no implicit newest key, environment fallback or remote key lookup.
type Signer struct {
	environment string
	keys        map[string]ed25519.PrivateKey
}

// String redacts retained private signing material.
func (s Signer) String() string               { return redacted }
func (s Signer) GoString() string             { return redacted }
func (s Signer) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (s Signer) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

// NewSigner validates/copies a bounded key ring. Private keys must be complete,
// self-consistent Ed25519 keys. Secret provenance/purpose separation is operational.
func NewSigner(environment string, keys map[string]ed25519.PrivateKey) (*Signer, error) {
	if !label(environment, 64) || len(keys) < 1 || len(keys) > 16 {
		return nil, ErrConfiguration
	}
	s := &Signer{environment: environment, keys: make(map[string]ed25519.PrivateKey, len(keys))}
	for id, key := range keys {
		if !label(id, 32) || len(key) != ed25519.PrivateKeySize {
			return nil, ErrConfiguration
		}
		expected := ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])
		if subtle.ConstantTimeCompare(key, expected) != 1 {
			return nil, ErrConfiguration
		}
		s.keys[id] = append(ed25519.PrivateKey(nil), key...)
	}
	return s, nil
}

// Sign uses the persisted key ID and scope, deterministically across attempts.
// It does not authorize a submission or persist any delivery metadata.
func (s *Signer) Sign(keyID string, scope Scope) (Capability, error) {
	if s == nil || !scope.valid() {
		return Capability{}, ErrInvalid
	}
	key, ok := s.keys[keyID]
	if !ok {
		return Capability{}, ErrInvalid
	}
	header := "r1." + keyID + "." + base64.RawURLEncoding.EncodeToString(scope.payload())
	signature := ed25519.Sign(key, []byte(domain+s.environment+"\x00"+header))
	return Capability{environment: s.environment, scope: scope, wire: header + "." + base64.RawURLEncoding.EncodeToString(signature)}, nil
}

// Verifier has public keys only. Removing a key revokes its capabilities; retain
// approved overlap keys until their observation windows and recovery gates permit removal.
type Verifier struct {
	environment string
	keys        map[string]ed25519.PublicKey
}

// NewVerifier validates/copies a bounded public-only key ring for one environment.
func NewVerifier(environment string, keys map[string]ed25519.PublicKey) (*Verifier, error) {
	if !label(environment, 64) || len(keys) < 1 || len(keys) > 16 {
		return nil, ErrConfiguration
	}
	v := &Verifier{environment: environment, keys: make(map[string]ed25519.PublicKey, len(keys))}
	for id, key := range keys {
		if !label(id, 32) || !cryptokey.StrongEd25519PublicKey(key) {
			return nil, ErrConfiguration
		}
		v.keys[id] = append(ed25519.PublicKey(nil), key...)
	}
	return v, nil
}

// Verify checks the signature against exact persisted scope before distinguishing
// expiry. The receipt service must separately authenticate and lock/recheck current
// owner authority before exposing that distinction or recording any observation.
func (v *Verifier) Verify(token string, expected Scope, now time.Time) error {
	if v == nil || !expected.valid() || now.IsZero() || len(token) > maxTokenSize {
		return ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "r1" || !label(parts[1], 32) {
		return ErrInvalid
	}
	key, ok := v.keys[parts[1]]
	if !ok {
		return ErrInvalid
	}
	payload, ok := decode(parts[2], payloadSize)
	if !ok || subtle.ConstantTimeCompare(payload, expected.payload()) != 1 {
		return ErrInvalid
	}
	signature, ok := decode(parts[3], ed25519.SignatureSize)
	if !ok || !ed25519.Verify(key, []byte(domain+v.environment+"\x00"+strings.Join(parts[:3], ".")), signature) {
		return ErrInvalid
	}
	if now.Unix() >= expected.ExpiresUnix {
		return ErrExpired
	}
	return nil
}
func decode(text string, size int) ([]byte, bool) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(text)
	return decoded, err == nil && len(decoded) == size && base64.RawURLEncoding.EncodeToString(decoded) == text
}
func label(value string, limit int) bool {
	if len(value) == 0 || len(value) > limit {
		return false
	}
	for _, b := range []byte(value) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}
