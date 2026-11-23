package onboarding

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/jsonobject"
)

const serverPolicyLimit = 64 * 1024

// ErrInvalidServerPolicy reports an invalid onboarding policy without exposing secret material.
var ErrInvalidServerPolicy = errors.New("invalid onboarding server policy")

// ServerPolicy contains the provisioner trust, client identity, and approved enrollment profiles.
type ServerPolicy struct {
	FingerprintKey        []byte
	FingerprintKeyVersion int32
	ProvisionerURL        string
	CA                    []byte
	ClientCertificate     []byte
	ClientPrivateKey      []byte
	Profiles              []ProfilePolicy
}

// ProfilePolicy describes one enrollment profile and its allowed platform identifiers.
type ProfilePolicy struct {
	ID             string
	Name           string
	Platforms      []PlatformPolicy
	StagingAppID   string
	StagingProject string
}

// PlatformPolicy describes the allowed application identifiers for a platform.
type PlatformPolicy struct {
	Platform               Platform
	ApplicationIdentifiers []string
}

// ParseServerPolicy parses a bounded, closed-schema onboarding policy.
func ParseServerPolicy(data []byte) (*ServerPolicy, error) {
	if len(data) == 0 || len(data) > serverPolicyLimit || !utf8.Valid(data) || !jsonobject.UniqueLowercaseFields(data) {
		return nil, ErrInvalidServerPolicy
	}
	var policy ServerPolicy
	var caPEM, clientCertificatePEM, clientPrivateKeyPEM string
	var profileObjects []json.RawMessage
	if jsonobject.Decode(data, map[string]any{
		"fingerprint_key":         &policy.FingerprintKey,
		"fingerprint_key_version": &policy.FingerprintKeyVersion,
		"provisioner_url":         &policy.ProvisionerURL,
		"ca_pem":                  &caPEM,
		"client_certificate_pem":  &clientCertificatePEM,
		"client_private_key_pem":  &clientPrivateKeyPEM,
		"profiles":                &profileObjects,
	}) != nil || len(policy.FingerprintKey) != 32 || policy.FingerprintKeyVersion <= 0 ||
		!hasFingerprintDiversity(policy.FingerprintKey) || !validProvisionerURL(policy.ProvisionerURL) || len(profileObjects) > 100 {
		return nil, ErrInvalidServerPolicy
	}
	ca := []byte(caPEM)
	clientCertificate := []byte(clientCertificatePEM)
	clientPrivateKey := []byte(clientPrivateKeyPEM)
	if _, ok := parseCertificates(ca, true); !ok || !validClientIdentity(clientCertificate, clientPrivateKey) {
		return nil, ErrInvalidServerPolicy
	}
	policy.CA = ca
	policy.ClientCertificate = clientCertificate
	policy.ClientPrivateKey = clientPrivateKey
	policy.Profiles = make([]ProfilePolicy, 0, len(profileObjects))
	seenProfiles := make(map[string]struct{}, len(profileObjects))
	for _, raw := range profileObjects {
		profile, ok := parseProfile(raw)
		if !ok {
			return nil, ErrInvalidServerPolicy
		}
		if _, exists := seenProfiles[profile.ID]; exists {
			return nil, ErrInvalidServerPolicy
		}
		seenProfiles[profile.ID] = struct{}{}
		policy.Profiles = append(policy.Profiles, profile)
	}
	return &policy, nil
}

// TLSConfig returns fresh TLS 1.3 trust and client identity state for the provisioner.
func (p *ServerPolicy) TLSConfig() (*tls.Config, error) {
	if p == nil || !validProvisionerURL(p.ProvisionerURL) {
		return nil, ErrInvalidServerPolicy
	}
	certificates, ok := parseCertificates(p.CA, true)
	if !ok || !validClientIdentity(p.ClientCertificate, p.ClientPrivateKey) {
		return nil, ErrInvalidServerPolicy
	}
	roots := x509.NewCertPool()
	for _, certificate := range certificates {
		roots.AddCert(certificate)
	}
	if len(certificates) == 0 {
		return nil, ErrInvalidServerPolicy
	}
	identity, err := tls.X509KeyPair(bytes.Clone(p.ClientCertificate), bytes.Clone(p.ClientPrivateKey))
	if err != nil {
		return nil, ErrInvalidServerPolicy
	}
	endpoint, _ := url.Parse(p.ProvisionerURL)
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		ServerName:   endpoint.Hostname(),
		Certificates: []tls.Certificate{identity},
	}, nil
}

func parseProfile(data []byte) (ProfilePolicy, bool) {
	var profile ProfilePolicy
	var platforms []json.RawMessage
	if jsonobject.DecodeOptional(data, map[string]any{
		"id":        &profile.ID,
		"name":      &profile.Name,
		"platforms": &platforms,
	}, map[string]any{"staging_app_id": &profile.StagingAppID, "staging_project_id": &profile.StagingProject}) != nil ||
		!canonicalPolicyUUID(profile.ID) || !validPolicyText(profile.Name, 1, 128) || len(platforms) < 1 || len(platforms) > 2 ||
		(profile.StagingAppID != "" && (!canonicalPolicyUUID(profile.StagingAppID) || profile.StagingProject == "")) ||
		(profile.StagingProject != "" && !validPolicyText(profile.StagingProject, 1, 128)) {
		return ProfilePolicy{}, false
	}
	profile.Platforms = make([]PlatformPolicy, 0, len(platforms))
	seenPlatforms := make(map[Platform]struct{}, len(platforms))
	for _, raw := range platforms {
		var platform PlatformPolicy
		if jsonobject.Decode(raw, map[string]any{
			"platform":                &platform.Platform,
			"application_identifiers": &platform.ApplicationIdentifiers,
		}) != nil || (platform.Platform != PlatformAndroid && platform.Platform != PlatformIOS) ||
			len(platform.ApplicationIdentifiers) < 1 || len(platform.ApplicationIdentifiers) > 100 {
			return ProfilePolicy{}, false
		}
		if _, exists := seenPlatforms[platform.Platform]; exists {
			return ProfilePolicy{}, false
		}
		seenPlatforms[platform.Platform] = struct{}{}
		seenIdentifiers := make(map[string]struct{}, len(platform.ApplicationIdentifiers))
		for _, identifier := range platform.ApplicationIdentifiers {
			if !validPolicyText(identifier, 1, 255) {
				return ProfilePolicy{}, false
			}
			if _, exists := seenIdentifiers[identifier]; exists {
				return ProfilePolicy{}, false
			}
			seenIdentifiers[identifier] = struct{}{}
		}
		profile.Platforms = append(profile.Platforms, platform)
	}
	return profile, true
}

func hasFingerprintDiversity(key []byte) bool {
	unique := make(map[byte]struct{}, len(key))
	for _, value := range key {
		unique[value] = struct{}{}
	}
	return len(unique) >= 8
}

func validProvisionerURL(value string) bool {
	endpoint, err := url.Parse(value)
	return err == nil && endpoint.Scheme == "https" && endpoint.Host != "" && endpoint.Hostname() != "" &&
		endpoint.User == nil && (endpoint.Path == "" || endpoint.Path == "/") && endpoint.RawPath == "" &&
		!endpoint.ForceQuery && endpoint.RawQuery == "" && endpoint.Fragment == "" && endpoint.Opaque == "" &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

func parseCertificates(data []byte, requireCA bool) ([]*x509.Certificate, bool) {
	remaining := bytes.TrimSpace(data)
	certificates := make([]*x509.Certificate, 0, 1)
	for len(remaining) > 0 {
		remaining = bytes.TrimLeft(remaining, " \t\r\n")
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, false
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, false
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !certificate.NotAfter.After(time.Now()) || certificate.NotBefore.After(time.Now()) {
			return nil, false
		}
		if requireCA && (!certificate.IsCA || !certificate.BasicConstraintsValid || certificate.KeyUsage&x509.KeyUsageCertSign == 0) {
			return nil, false
		}
		certificates = append(certificates, certificate)
		remaining = bytes.TrimSpace(rest)
	}
	return certificates, len(certificates) > 0
}

func validClientIdentity(certificatePEM, privateKeyPEM []byte) bool {
	certificates, ok := parseCertificates(certificatePEM, false)
	if !ok || !validPrivateKeyPEM(privateKeyPEM) {
		return false
	}
	identity, err := tls.X509KeyPair(bytes.Clone(certificatePEM), bytes.Clone(privateKeyPEM))
	if err != nil || len(identity.Certificate) != len(certificates) {
		return false
	}
	leaf := certificates[0]
	if leaf.IsCA || !hasExtendedKeyUsage(leaf, x509.ExtKeyUsageClientAuth) || len(leaf.URIs) == 0 {
		return false
	}
	for _, identity := range leaf.URIs {
		if identity != nil && identity.IsAbs() && identity.Host != "" && identity.User == nil && identity.RawQuery == "" && identity.Fragment == "" {
			return true
		}
	}
	return false
}

func validPrivateKeyPEM(data []byte) bool {
	remaining := bytes.TrimSpace(data)
	if !bytes.HasPrefix(remaining, []byte("-----BEGIN ")) {
		return false
	}
	block, rest := pem.Decode(remaining)
	if block == nil || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return false
	}
	switch block.Type {
	case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
		return true
	default:
		return false
	}
}

func hasExtendedKeyUsage(certificate *x509.Certificate, wanted x509.ExtKeyUsage) bool {
	for _, usage := range certificate.ExtKeyUsage {
		if usage == wanted || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

func canonicalPolicyUUID(value string) bool {
	var id pgtype.UUID
	return len(value) == 36 && id.Scan(value) == nil && id.Valid && id.Bytes != [16]byte{} && id.String() == value
}

func validPolicyText(value string, min, max int) bool {
	length := utf8.RuneCountInString(value)
	if length < min || length > max {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
