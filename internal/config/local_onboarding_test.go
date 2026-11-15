package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"hello/internal/deployment"
)

func TestLocalServerOnboardingUsesFixedProtectedPathAndFailsClosed(t *testing.T) {
	base := localOnboardingBaseConfig()
	originalOnboarding := base.Onboarding
	for name, readSecret := range map[string]func(string) ([]byte, error){
		"read failure": func(path string) ([]byte, error) {
			if path != localOnboardingPolicyPath {
				t.Fatalf("read path = %q", path)
			}
			return nil, errors.New("fixture read failure")
		},
		"invalid policy": func(path string) ([]byte, error) {
			if path != localOnboardingPolicyPath {
				t.Fatalf("read path = %q", path)
			}
			return []byte(`{"unknown":true}`), nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := localServerOnboarding(base, readSecret)
			if result != nil || err != deployment.ErrConfiguration {
				t.Fatalf("localServerOnboarding() = (%p, %v)", result, err)
			}
			if base.Onboarding != originalOnboarding || base.DBHost != "db.local.test" ||
				base.DBPort != "5544" || base.DBUser != "local-user" || base.DBPassword != "local-password" ||
				base.DBName != "hello-local" || base.DBSSLMode != "disable" || base.APIKey != "local-api-key" {
				t.Fatal("failed onboarding load mutated the existing server configuration")
			}
		})
	}
}

func TestLocalServerOnboardingSetsPolicyAndPreservesBaseConfig(t *testing.T) {
	base := localOnboardingBaseConfig()
	protectedDB := base.protectedDatabase
	policyBytes := localOnboardingPolicyFixture(t)
	readPath := ""
	result, err := localServerOnboarding(base, func(path string) ([]byte, error) {
		readPath = path
		return policyBytes, nil
	})
	if err != nil || result != base || readPath != localOnboardingPolicyPath {
		t.Fatalf("localServerOnboarding() = (%p, %v), read path %q", result, err, readPath)
	}
	if result.Onboarding == nil || result.Onboarding.FingerprintKeyVersion != 2 || len(result.Onboarding.Profiles) != 1 ||
		result.Onboarding.Profiles[0].ID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected onboarding policy: %#v", result.Onboarding)
	}
	if result.BaseConfig != base.BaseConfig || result.APIKey != "local-api-key" || result.ListenAddress != "127.0.0.1:8123" ||
		result.AccessSecret != "local-access" || result.RefreshSecret != "local-refresh" || result.protectedDatabase != protectedDB {
		t.Fatal("valid onboarding load changed unrelated server configuration")
	}
}

func TestServerForCommandRejectsLocalOnboardingPathOverrideBeforeEnvironmentLoad(t *testing.T) {
	if os.Getenv("HELLO_TEST_LOCAL_ONBOARDING_OVERRIDE") == "1" {
		_, err := ServerForCommand([]string{"--local-onboarding", "/tmp/alternate-policy.json"})
		if err != deployment.ErrConfiguration {
			os.Exit(41)
		}
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestServerForCommandRejectsLocalOnboardingPathOverrideBeforeEnvironmentLoad$")
	cmd.Env = []string{"HELLO_TEST_LOCAL_ONBOARDING_OVERRIDE=1", "PATH=" + os.Getenv("PATH")}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("invalid onboarding path argument reached environment loading or was accepted: %v (%s)", err, strings.TrimSpace(string(output)))
	}
}

func localOnboardingBaseConfig() *ServerConfig {
	return &ServerConfig{
		BaseConfig:    BaseConfig{DBHost: "db.local.test", DBPort: "5544", DBUser: "local-user", DBPassword: "local-password", DBName: "hello-local", DBSSLMode: "disable"},
		ListenAddress: "127.0.0.1:8123", APIKey: "local-api-key", AccessSecret: "local-access", RefreshSecret: "local-refresh",
	}
}

func localOnboardingPolicyFixture(t *testing.T) []byte {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "local onboarding test CA"},
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
	identity, err := url.Parse("spiffe://provisioner.local.test/client/onboarding")
	if err != nil {
		t.Fatal(err)
	}
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "local onboarding test client"},
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
	policy := map[string]any{
		"fingerprint_key":         []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
		"fingerprint_key_version": 2, "provisioner_url": "https://provisioner.local.test/",
		"ca_pem": string(caPEM), "client_certificate_pem": string(clientPEM), "client_private_key_pem": string(privatePEM),
		"profiles": []map[string]any{{"id": "11111111-1111-4111-8111-111111111111", "name": "Local profile", "platforms": []map[string]any{{"platform": "ANDROID", "application_identifiers": []string{"com.example.local"}}}}},
	}
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
