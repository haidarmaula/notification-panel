package token

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"hello/internal/jsonobject"

	"github.com/golang-jwt/jwt/v5"
)

// ErrPolicy contains no secret, selected key ID or supplied configuration.
var ErrPolicy = errors.New("invalid staff token policy")

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

type hmacKey struct {
	secret      []byte
	verifyUntil time.Time
}

type purposeKeys struct {
	audience string
	active   string
	keys     map[string]hmacKey
}

// ParsePolicy builds an immutable, purpose-specific HS256 issuer/verifier.
// Only the protected server loader supplies the policy and its API secret.
func ParsePolicy(data []byte, apiSecret string) (*TokenManager, error) {
	var issuer string
	var access, refresh json.RawMessage
	if jsonobject.Decode(data, map[string]any{"issuer": &issuer, "access": &access, "refresh": &refresh}) != nil || !identityLabel(issuer) {
		return nil, ErrPolicy
	}
	seenIDs := make(map[string]bool)
	seenKeys := map[string]bool{apiSecret: true}
	for _, encoding := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if decoded, err := encoding.Strict().DecodeString(apiSecret); err == nil {
			seenKeys[string(decoded)] = true
		}
	}
	if decoded, err := hex.DecodeString(apiSecret); err == nil {
		seenKeys[string(decoded)] = true
	}
	accessKeys, err := parsePurpose(access, seenIDs, seenKeys)
	if err != nil {
		return nil, err
	}
	refreshKeys, err := parsePurpose(refresh, seenIDs, seenKeys)
	if err != nil || accessKeys.audience == refreshKeys.audience {
		return nil, ErrPolicy
	}
	return &TokenManager{issuer: issuer, access: accessKeys, refresh: refreshKeys}, nil
}

func identityLabel(value string) bool {
	if len(value) < 1 || len(value) > 255 {
		return false
	}
	for i := range len(value) {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func parsePurpose(raw json.RawMessage, seenIDs, seenKeys map[string]bool) (purposeKeys, error) {
	var p purposeKeys
	var entries []json.RawMessage
	if jsonobject.Decode(raw, map[string]any{"audience": &p.audience, "active_kid": &p.active, "keys": &entries}) != nil ||
		!identityLabel(p.audience) || !keyIDPattern.MatchString(p.active) || len(entries) < 1 || len(entries) > 8 {
		return purposeKeys{}, ErrPolicy
	}
	p.keys = make(map[string]hmacKey, len(entries))
	for _, entry := range entries {
		var id, encoded string
		var until *string
		if jsonobject.DecodeOptional(entry, map[string]any{"kid": &id, "key": &encoded}, map[string]any{"verify_until": &until}) != nil ||
			!keyIDPattern.MatchString(id) || seenIDs[id] || len(encoded) != 43 {
			return purposeKeys{}, ErrPolicy
		}
		secret, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
		if err != nil || len(secret) != 32 || seenKeys[string(secret)] || [32]byte(secret) == [32]byte{} {
			return purposeKeys{}, ErrPolicy
		}
		key := hmacKey{secret: secret}
		if id == p.active {
			if until != nil {
				return purposeKeys{}, ErrPolicy
			}
		} else {
			if until == nil {
				return purposeKeys{}, ErrPolicy
			}
			key.verifyUntil, err = time.Parse(time.RFC3339, *until)
			if err != nil || key.verifyUntil.IsZero() || key.verifyUntil.UTC().Format(time.RFC3339) != *until {
				return purposeKeys{}, ErrPolicy
			}
		}
		p.keys[id] = key
		seenIDs[id] = true
		seenKeys[string(secret)] = true
	}
	if _, ok := p.keys[p.active]; !ok {
		return purposeKeys{}, ErrPolicy
	}
	return p, nil
}

func (p purposeKeys) sign(claims jwt.Claims) (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	t.Header["kid"] = p.active
	key := p.keys[p.active]
	if len(key.secret) == 0 {
		return "", ErrInvalidToken
	}
	return t.SignedString(key.secret)
}

func (p purposeKeys) keyFor(t *jwt.Token) (any, error) {
	id, ok := t.Header["kid"].(string)
	if !ok || t.Method != jwt.SigningMethodHS256 || t.Header["typ"] != "JWT" || len(t.Header) != 3 {
		return nil, ErrInvalidToken
	}
	key, ok := p.keys[id]
	if !ok || len(key.secret) == 0 || (!key.verifyUntil.IsZero() && !time.Now().Before(key.verifyUntil)) {
		return nil, ErrInvalidToken
	}
	return key.secret, nil
}

func localPurpose(audience, id, secret string) purposeKeys {
	return purposeKeys{audience: audience, active: id, keys: map[string]hmacKey{id: {secret: []byte(secret)}}}
}
