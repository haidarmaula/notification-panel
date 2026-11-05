package receipt

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func TestPolicyAndMaterialRequireExplicitSupportedValues(t *testing.T) {
	valid := Policy{Environment: "fixture", KeyID: "key", ObservationWindow: time.Hour}
	if valid.Validate() != nil {
		t.Fatal("bounded explicit policy rejected")
	}
	for _, change := range []func(*Policy){func(p *Policy) { p.Environment = "" }, func(p *Policy) { p.KeyID = "bad.key" }, func(p *Policy) { p.ObservationWindow = 0 }, func(p *Policy) { p.ObservationWindow += time.Nanosecond }, func(p *Policy) { p.ObservationWindow = 91 * 24 * time.Hour }} {
		p := valid
		change(&p)
		if p.Validate() == nil {
			t.Fatal("unsafe receipt policy accepted")
		}
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))
	signer, _ := NewSigner("fixture", map[string]ed25519.PrivateKey{"key": key})
	material := Material{Version: 1, Environment: "fixture", KeyID: "key", Scope: fixtureScope()}
	capability, err := signer.SignMaterial(material)
	if err != nil || !capability.Matches("fixture", material.Scope) {
		t.Fatal("committed material rejected")
	}
	for _, change := range []func(*Material){func(m *Material) { m.Version = 2 }, func(m *Material) { m.Environment = "other" }, func(m *Material) { m.KeyID = "removed" }, func(m *Material) { m.Scope.OwnerEpoch = 0 }} {
		changed := material
		change(&changed)
		if _, err := signer.SignMaterial(changed); !errors.Is(err, ErrInvalid) {
			t.Fatal("material/signing policy confusion accepted")
		}
	}
}
