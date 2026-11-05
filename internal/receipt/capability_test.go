package receipt

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func fixtureScope() Scope {
	id := func(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }
	return Scope{ApplicationID: id(1), OriginEpoch: id(2), NotificationID: 3, DeliveryID: id(4), BindingID: 5, InstallationID: id(6), OwnerEpoch: 7, ExpiresUnix: 2000000000}
}
func TestCapabilityRoundTripAndDeterminism(t *testing.T) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))
	signer, err := NewSigner("test", map[string]ed25519.PrivateKey{"key1": key})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier("test", map[string]ed25519.PublicKey{"key1": key.Public().(ed25519.PublicKey)})
	if err != nil {
		t.Fatal(err)
	}
	scope := fixtureScope()
	first, err := signer.Sign("key1", scope)
	if err != nil {
		t.Fatal(err)
	}
	second, err := signer.Sign("key1", scope)
	if err != nil {
		t.Fatal(err)
	}
	if first.WireValue() != second.WireValue() {
		t.Fatal("retry changed capability")
	}
	if !first.Matches("test", scope) {
		t.Fatal("scope mismatch")
	}
	if err := verifier.Verify(first.WireValue(), scope, time.Unix(scope.ExpiresUnix-1, 0)); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityScopeEnvironmentAndTampering(t *testing.T) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32))
	signer, _ := NewSigner("production", map[string]ed25519.PrivateKey{"k": key})
	verifier, _ := NewVerifier("production", map[string]ed25519.PublicKey{"k": key.Public().(ed25519.PublicKey)})
	scope := fixtureScope()
	capability, _ := signer.Sign("k", scope)
	now := time.Unix(scope.ExpiresUnix-1, 0)
	for _, change := range []func(*Scope){
		func(s *Scope) { s.ApplicationID.Bytes[0]++ }, func(s *Scope) { s.OriginEpoch.Bytes[0]++ }, func(s *Scope) { s.NotificationID++ },
		func(s *Scope) { s.DeliveryID.Bytes[0]++ }, func(s *Scope) { s.BindingID++ }, func(s *Scope) { s.InstallationID.Bytes[0]++ },
		func(s *Scope) { s.OwnerEpoch++ }, func(s *Scope) { s.ExpiresUnix++ },
	} {
		other := scope
		change(&other)
		if !errors.Is(verifier.Verify(capability.WireValue(), other, now), ErrInvalid) {
			t.Fatal("accepted changed scope")
		}
	}
	otherEnvironment, _ := NewVerifier("staging", map[string]ed25519.PublicKey{"k": key.Public().(ed25519.PublicKey)})
	if !errors.Is(otherEnvironment.Verify(capability.WireValue(), scope, now), ErrInvalid) {
		t.Fatal("accepted foreign environment")
	}
	for _, token := range []string{"", capability.WireValue() + ".", strings.Replace(capability.WireValue(), "r1.", "r2.", 1), strings.Replace(capability.WireValue(), ".k.", ".other.", 1), strings.Repeat("x", 257)} {
		if !errors.Is(verifier.Verify(token, scope, now), ErrInvalid) {
			t.Fatal("accepted malformed capability")
		}
	}
	parts := strings.Split(capability.WireValue(), ".")
	for _, index := range []int{2, 3} {
		changed := append([]string(nil), parts...)
		raw, _ := base64.RawURLEncoding.DecodeString(changed[index])
		raw[0] ^= 1
		changed[index] = base64.RawURLEncoding.EncodeToString(raw)
		if !errors.Is(verifier.Verify(strings.Join(changed, "."), scope, now), ErrInvalid) {
			t.Fatal("accepted tampering")
		}
		changed = append([]string(nil), parts...)
		changed[index] += "="
		if !errors.Is(verifier.Verify(strings.Join(changed, "."), scope, now), ErrInvalid) {
			t.Fatal("accepted padded encoding")
		}
		changed = append([]string(nil), parts...)
		changed[index] += "\n"
		if !errors.Is(verifier.Verify(strings.Join(changed, "."), scope, now), ErrInvalid) {
			t.Fatal("accepted newline encoding")
		}
	}
	alias := append([]string(nil), parts...)
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := alias[3][len(alias[3])-1]
	index := strings.IndexByte(alphabet, last)
	alias[3] = alias[3][:len(alias[3])-1] + string(alphabet[index+1])
	if !errors.Is(verifier.Verify(strings.Join(alias, "."), scope, now), ErrInvalid) {
		t.Fatal("accepted nonzero base64 padding bits")
	}
	if !errors.Is(verifier.Verify(capability.WireValue(), scope, time.Unix(scope.ExpiresUnix, 0)), ErrExpired) {
		t.Fatal("expiry boundary accepted")
	}
	changed := parts
	raw, _ := base64.RawURLEncoding.DecodeString(changed[3])
	raw[0] ^= 1
	changed[3] = base64.RawURLEncoding.EncodeToString(raw)
	if !errors.Is(verifier.Verify(strings.Join(changed, "."), scope, time.Unix(scope.ExpiresUnix+1, 0)), ErrInvalid) {
		t.Fatal("expiry oracle precedes signature")
	}
}

func TestCapabilityKeyRotationCopiesAndRedaction(t *testing.T) {
	old := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, 32))
	current := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{4}, 32))
	private := map[string]ed25519.PrivateKey{"old": old, "new": current}
	public := map[string]ed25519.PublicKey{"old": append(ed25519.PublicKey(nil), old.Public().(ed25519.PublicKey)...), "new": current.Public().(ed25519.PublicKey)}
	signer, err := NewSigner("test", private)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier("test", public)
	if err != nil {
		t.Fatal(err)
	}
	scope := fixtureScope()
	before, _ := signer.Sign("old", scope)
	old[0] ^= 0xff
	public["old"][0] ^= 0xff
	delete(private, "old")
	delete(public, "old")
	after, err := signer.Sign("old", scope)
	if err != nil || before.WireValue() != after.WireValue() {
		t.Fatal("configuration mutation changed signer")
	}
	if err := verifier.Verify(after.WireValue(), scope, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	revoked, _ := NewVerifier("test", map[string]ed25519.PublicKey{"new": current.Public().(ed25519.PublicKey)})
	if !errors.Is(revoked.Verify(after.WireValue(), scope, time.Unix(1, 0)), ErrInvalid) {
		t.Fatal("removed key remained valid")
	}
	if _, err := signer.Sign("unknown", scope); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown signing key accepted")
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("fixture", "capability", after)
	jsonValue, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{fmt.Sprintf("%v %+v %#v", after, after, after), logs.String(), string(jsonValue)} {
		if strings.Contains(output, after.WireValue()) || !strings.Contains(output, "REDACTED") {
			t.Fatal("capability escaped redaction")
		}
	}
}

func TestCapabilityRejectsInvalidConfigurationAndScope(t *testing.T) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{5}, 32))
	for _, environment := range []string{"", "prod.test", strings.Repeat("a", 65), "prod\n"} {
		if _, err := NewSigner(environment, map[string]ed25519.PrivateKey{"k": key}); !errors.Is(err, ErrConfiguration) {
			t.Fatal("invalid environment accepted")
		}
	}
	corrupt := append(ed25519.PrivateKey(nil), key...)
	corrupt[63] ^= 1
	for _, keys := range []map[string]ed25519.PrivateKey{nil, {"bad.key": key}, {"k": key[:32]}, {"k": corrupt}} {
		if _, err := NewSigner("test", keys); !errors.Is(err, ErrConfiguration) {
			t.Fatal("invalid key ring accepted")
		}
	}
	signer, _ := NewSigner("test", map[string]ed25519.PrivateKey{"k": key})
	for _, change := range []func(*Scope){func(s *Scope) { s.OwnerEpoch = 0 }, func(s *Scope) { s.BindingID = -1 }, func(s *Scope) { s.ExpiresUnix = maxExpiryUnix + 1 }, func(s *Scope) { s.DeliveryID = pgtype.UUID{} }} {
		scope := fixtureScope()
		change(&scope)
		if _, err := signer.Sign("k", scope); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid scope accepted")
		}
	}
	if (Capability{}).Matches("test", fixtureScope()) {
		t.Fatal("zero capability matched")
	}
}

func FuzzCapabilityVerification(f *testing.F) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{6}, 32))
	signer, _ := NewSigner("fuzz", map[string]ed25519.PrivateKey{"k": key})
	verifier, _ := NewVerifier("fuzz", map[string]ed25519.PublicKey{"k": key.Public().(ed25519.PublicKey)})
	scope := fixtureScope()
	capability, _ := signer.Sign("k", scope)
	f.Add(capability.WireValue())
	f.Add("")
	f.Add("r1.k.AAAA.AAAA")
	f.Fuzz(func(t *testing.T, token string) {
		err := verifier.Verify(token, scope, time.Unix(1, 0))
		if err == nil && token != capability.WireValue() {
			t.Fatal("noncanonical or forged token verified")
		}
	})
}

func TestCapabilityWireLayout(t *testing.T) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	signer, _ := NewSigner("wire", map[string]ed25519.PrivateKey{"k": key})
	token, _ := signer.Sign("k", fixtureScope())
	parts := strings.Split(token.WireValue(), ".")
	body, _ := base64.RawURLEncoding.DecodeString(parts[2])
	want := "01000000000000000000000000000000" + "02000000000000000000000000000000" + "0000000000000003" + "04000000000000000000000000000000" + "0000000000000005" + "06000000000000000000000000000000" + "0000000000000007" + "0000000077359400"
	if hex.EncodeToString(body) != want {
		t.Fatal("wire field layout changed")
	}
	signature, _ := base64.RawURLEncoding.DecodeString(parts[3])
	preimage := []byte("hello/receipt-capability/v1\x00wire\x00" + strings.Join(parts[:3], "."))
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), preimage, signature) {
		t.Fatal("wire signature domain changed")
	}
}

func TestSignerCopiedValueRedactsKeys(t *testing.T) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, 32))
	signer, _ := NewSigner("test", map[string]ed25519.PrivateKey{"k": key})
	for _, value := range []any{signer, *signer} {
		output := fmt.Sprintf("%v %+v %#v", value, value, value)
		if strings.Count(output, "REDACTED") != 3 {
			t.Fatal("copied signer exposes private fields")
		}
		var logs bytes.Buffer
		slog.New(slog.NewJSONHandler(&logs, nil)).Info("fixture", "signer", value)
		if !strings.Contains(logs.String(), "REDACTED") {
			t.Fatal("copied signer escaped structured redaction")
		}
		data, err := json.Marshal(value)
		if err != nil || string(data) != `"[REDACTED]"` {
			t.Fatal("copied signer escaped JSON redaction")
		}
	}
}

func TestVerifierRejectsIdentityKeyForgery(t *testing.T) {
	key := make(ed25519.PublicKey, 32)
	key[0] = 1
	signature := make([]byte, 64)
	signature[0] = 1
	scope := fixtureScope()
	header := "r1.identity." + base64.RawURLEncoding.EncodeToString(scope.payload())
	if !ed25519.Verify(key, []byte(domain+"test\x00"+header), signature) {
		t.Fatal("stdlib behavior changed; revisit forged-capability regression")
	}
	if verifier, err := NewVerifier("test", map[string]ed25519.PublicKey{"identity": key}); verifier != nil || !errors.Is(err, ErrConfiguration) {
		t.Fatal("accepted universal-forgery key")
	}
}
