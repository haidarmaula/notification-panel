package onboarding

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
)

const policyTestProfileID = "11111111-1111-4111-8111-111111111111"

type serverPolicyWire struct {
	FingerprintKey        []byte              `json:"fingerprint_key"`
	FingerprintKeyVersion int32               `json:"fingerprint_key_version"`
	ProvisionerURL        string              `json:"provisioner_url"`
	CAPEM                 string              `json:"ca_pem"`
	ClientCertificatePEM  string              `json:"client_certificate_pem"`
	ClientPrivateKeyPEM   string              `json:"client_private_key_pem"`
	Profiles              []profilePolicyWire `json:"profiles"`
}

type profilePolicyWire struct {
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	Platforms []platformPolicyWire `json:"platforms"`
}

type platformPolicyWire struct {
	Platform               string   `json:"platform"`
	ApplicationIdentifiers []string `json:"application_identifiers"`
}

func policyFixture(t *testing.T) serverPolicyWire {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "policy test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	clientPublic, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := url.Parse("spiffe://provisioner.example.test/client/onboarding")
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "onboarding client"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs: []*url.URL{identity},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caTemplate, clientPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	clientPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER})
	privateDER, err := x509.MarshalPKCS8PrivateKey(clientPrivate)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	return serverPolicyWire{
		FingerprintKey:        []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
		FingerprintKeyVersion: 1,
		ProvisionerURL:        "https://provisioner.example.test/",
		CAPEM:                 string(caPEM), ClientCertificatePEM: string(clientPEM), ClientPrivateKeyPEM: string(privatePEM),
		Profiles: []profilePolicyWire{{ID: policyTestProfileID, Name: "Default", Platforms: []platformPolicyWire{{
			Platform: "ANDROID", ApplicationIdentifiers: []string{"com.example.app"},
		}}}},
	}
}

func marshalPolicy(t *testing.T, wire serverPolicyWire) []byte {
	t.Helper()
	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseServerPolicyAndTLSConfig(t *testing.T) {
	policy, err := ParseServerPolicy(marshalPolicy(t, policyFixture(t)))
	if err != nil {
		t.Fatalf("ParseServerPolicy(): %v", err)
	}
	if policy.FingerprintKeyVersion != 1 || len(policy.FingerprintKey) != 32 || len(policy.Profiles) != 1 ||
		policy.Profiles[0].Platforms[0].Platform != PlatformAndroid || policy.Profiles[0].Platforms[0].ApplicationIdentifiers[0] != "com.example.app" {
		t.Fatalf("unexpected parsed policy: %#v", policy)
	}
	first, err := policy.TLSConfig()
	if err != nil {
		t.Fatalf("TLSConfig(): %v", err)
	}
	if first.MinVersion != tls.VersionTLS13 || first.RootCAs == nil || first.ServerName != "provisioner.example.test" ||
		first.InsecureSkipVerify || len(first.Certificates) != 1 || len(first.Certificates[0].Certificate) != 1 || first.Certificates[0].PrivateKey == nil {
		t.Fatalf("unsafe or incomplete TLS config: %#v", first)
	}
	second, err := policy.TLSConfig()
	if err != nil {
		t.Fatalf("second TLSConfig(): %v", err)
	}
	if first.RootCAs == second.RootCAs || &first.Certificates[0].Certificate[0][0] == &second.Certificates[0].Certificate[0][0] {
		t.Fatal("TLSConfig returned shared mutable trust or certificate bytes")
	}
	first.Certificates[0].Certificate[0][0] ^= 0xff
	if _, err := x509.ParseCertificate(second.Certificates[0].Certificate[0]); err != nil {
		t.Fatalf("mutating one config affected another: %v", err)
	}
}

func TestParseServerPolicyRejectsMalformedAndNoncanonicalJSON(t *testing.T) {
	valid := marshalPolicy(t, policyFixture(t))
	duplicate := strings.TrimSuffix(string(valid), "}") + `,"fingerprint_key_version":1}`
	uppercaseAlias := strings.Replace(string(valid), `"fingerprint_key_version"`, `"Fingerprint_key_version"`, 1)
	unknown := strings.TrimSuffix(string(valid), "}") + `,"debug_secret":"secret-marker"}`
	nullWire := policyFixture(t)
	nullWire.Profiles = nil
	nullProfiles := marshalPolicy(t, nullWire)
	nestedDuplicate := strings.Replace(string(valid), `"name":"Default"`, `"name":"Default","name":"Second"`, 1)
	nestedAlias := strings.Replace(string(valid), `"platform":"ANDROID"`, `"Platform":"ANDROID"`, 1)
	for name, data := range map[string][]byte{
		"duplicate root key": []byte(duplicate),
		"case alias":         []byte(uppercaseAlias),
		"unknown root key":   []byte(unknown),
		"null array":         nullProfiles,
		"nested duplicate":   []byte(nestedDuplicate),
		"nested case alias":  []byte(nestedAlias),
		"trailing object":    append(bytesClone(valid), []byte(` {}`)...),
		"non-object":         []byte(`null`),
		"invalid UTF-8":      append([]byte(`{"`), 0xff),
		"oversized":          append(bytesClone(valid), make([]byte, serverPolicyLimit+1)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseServerPolicy(data); !errors.Is(err, ErrInvalidServerPolicy) {
				t.Fatalf("ParseServerPolicy() error = %v", err)
			}
		})
	}
}

func TestParseServerPolicyValidatesProfilesAndSecurityMaterial(t *testing.T) {
	base := policyFixture(t)
	tests := map[string]func(*serverPolicyWire){
		"fingerprint entropy":      func(p *serverPolicyWire) { p.FingerprintKey = []byte(strings.Repeat("a", 32)) },
		"fingerprint length":       func(p *serverPolicyWire) { p.FingerprintKey = []byte("short") },
		"fingerprint version":      func(p *serverPolicyWire) { p.FingerprintKeyVersion = 0 },
		"insecure provisioner URL": func(p *serverPolicyWire) { p.ProvisionerURL = "http://provisioner.example.test" },
		"URL userinfo":             func(p *serverPolicyWire) { p.ProvisionerURL = "https://user@provisioner.example.test" },
		"URL path":                 func(p *serverPolicyWire) { p.ProvisionerURL = "https://provisioner.example.test/stages" },
		"URL query":                func(p *serverPolicyWire) { p.ProvisionerURL = "https://provisioner.example.test?debug=1" },
		"profile ID":               func(p *serverPolicyWire) { p.Profiles[0].ID = "00000000-0000-0000-0000-000000000000" },
		"duplicate profile":        func(p *serverPolicyWire) { p.Profiles = append(p.Profiles, p.Profiles[0]) },
		"profile name control":     func(p *serverPolicyWire) { p.Profiles[0].Name = "bad\nname" },
		"duplicate platform": func(p *serverPolicyWire) {
			p.Profiles[0].Platforms = append(p.Profiles[0].Platforms, p.Profiles[0].Platforms[0])
		},
		"unsupported platform": func(p *serverPolicyWire) { p.Profiles[0].Platforms[0].Platform = "WEB" },
		"duplicate identifier": func(p *serverPolicyWire) {
			p.Profiles[0].Platforms[0].ApplicationIdentifiers = []string{"same", "same"}
		},
		"identifier control":                   func(p *serverPolicyWire) { p.Profiles[0].Platforms[0].ApplicationIdentifiers = []string{"bad\x00id"} },
		"invalid CA PEM":                       func(p *serverPolicyWire) { p.CAPEM += "trailing" },
		"key in CA PEM":                        func(p *serverPolicyWire) { p.CAPEM += p.ClientPrivateKeyPEM },
		"certificate in CA bundle is not a CA": func(p *serverPolicyWire) { p.CAPEM = p.ClientCertificatePEM },
		"client certificate wrong key": func(p *serverPolicyWire) {
			p.ClientPrivateKeyPEM = p.ClientPrivateKeyPEM[:len(p.ClientPrivateKeyPEM)-20] + "garbage"
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			wire := base
			wire.FingerprintKey = append([]byte(nil), base.FingerprintKey...)
			wire.Profiles = append([]profilePolicyWire(nil), base.Profiles...)
			wire.Profiles[0].Platforms = append([]platformPolicyWire(nil), base.Profiles[0].Platforms...)
			wire.Profiles[0].Platforms[0].ApplicationIdentifiers = append([]string(nil), base.Profiles[0].Platforms[0].ApplicationIdentifiers...)
			change(&wire)
			_, err := ParseServerPolicy(marshalPolicy(t, wire))
			if !errors.Is(err, ErrInvalidServerPolicy) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("ParseServerPolicy() error = %v", err)
			}
		})
	}
}

func TestParseServerPolicyRequiresClientAuthenticationIdentity(t *testing.T) {
	for name, invalid := range map[string]struct{ omitURI, serverOnly bool }{
		"no URI SAN":      {omitURI: true},
		"server-only EKU": {serverOnly: true},
	} {
		t.Run(name, func(t *testing.T) {
			wire := policyFixture(t)
			wire.ClientCertificatePEM, wire.ClientPrivateKeyPEM = clientPairWithPolicy(t, invalid.omitURI, invalid.serverOnly)
			if _, err := ParseServerPolicy(marshalPolicy(t, wire)); !errors.Is(err, ErrInvalidServerPolicy) {
				t.Fatalf("ParseServerPolicy() error = %v", err)
			}
		})
	}
}

func clientPairWithPolicy(t *testing.T, omitURI, serverOnly bool) (string, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	client := &x509.Certificate{
		SerialNumber: big.NewInt(9), Subject: pkix.Name{CommonName: "invalid policy client"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if serverOnly {
		client.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	if !omitURI {
		client.URIs = []*url.URL{{Scheme: "spiffe", Host: "provisioner.example.test", Path: "/client"}}
	}
	der, err := x509.CreateCertificate(rand.Reader, client, client, public, private)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return string(certPEM), string(keyPEM)
}

func bytesClone(value []byte) []byte { return append([]byte(nil), value...) }
