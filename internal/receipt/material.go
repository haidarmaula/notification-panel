package receipt

import (
	"strings"
	"time"
)

// Policy explicitly selects metadata for new deliveries. It supplies no default
// production observation window; existing frozen deliveries retain their values.
type Policy struct {
	Environment       string
	KeyID             string
	ObservationWindow time.Duration
}

// Validate bounds whole-second policy before SQL conversion. Key presence in the
// deployed signer must be checked separately before enabling provider execution.
func (p Policy) Validate() error {
	if !label(p.Environment, 64) || !label(p.KeyID, 32) || p.ObservationWindow < time.Second || p.ObservationWindow > 90*24*time.Hour || p.ObservationWindow%time.Second != 0 {
		return ErrConfiguration
	}
	return nil
}

// Material is committed version/key/environment metadata plus immutable delivery
// scope, not a provider-execution permission. Retrying must not replace its values.
type Material struct {
	Version     int16
	KeyID       string
	Environment string
	Scope       Scope
}

// Valid checks supported metadata without deciding current DB/mobile authority.
func (m Material) Valid() bool {
	return m.Version == 1 && label(m.KeyID, 32) && label(m.Environment, 64) && m.Scope.valid()
}

// SignMaterial uses the frozen deployment/key/scope rather than current policy.
// Missing overlap keys or a foreign restored environment fail closed.
func (s *Signer) SignMaterial(material Material) (Capability, error) {
	if s == nil || !material.Valid() || s.environment != material.Environment {
		return Capability{}, ErrInvalid
	}
	return s.Sign(material.KeyID, material.Scope)
}

// VerifyMaterial checks the specific environment and key frozen on the delivery,
// including when another overlapping public key is valid for the same scope.
func (v *Verifier) VerifyMaterial(token string, m Material, now time.Time) error {
	if v == nil || !m.Valid() || v.environment != m.Environment || !strings.HasPrefix(token, "r1."+m.KeyID+".") {
		return ErrInvalid
	}
	return v.Verify(token, m.Scope, now)
}
