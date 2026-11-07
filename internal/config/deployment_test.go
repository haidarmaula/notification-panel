package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hello/internal/deployment"
)

func TestEscapedDatabaseCredentials(t *testing.T) {
	for _, host := range []string{"localhost", "::1", "[::1]"} {
		c := BaseConfig{DBHost: host, DBPort: "5432", DBUser: "user@domain", DBPassword: "p@:/?#% +", DBName: "customer A#1", DBSSLMode: "verify-full"}
		u, err := url.Parse(c.GetDatabaseURL())
		if err != nil {
			t.Fatal(err)
		}
		password, ok := u.User.Password()
		if !ok || password != c.DBPassword || u.User.Username() != c.DBUser || u.Hostname() != strings.Trim(host, "[]") ||
			u.Port() != c.DBPort || u.Path != "/"+c.DBName || u.Query().Get("sslmode") != c.DBSSLMode || u.Fragment != "" {
			t.Fatal("DSN components did not round trip")
		}
	}
}

func TestProtectedServerDatabaseAndMobilePolicy(t *testing.T) {
	clearDeploymentEnv(t)
	const dbURL = "postgres://svc:p%40ss@db.example.test:5432/app?sslmode=verify-full"
	p := map[string]any{"ingress": fixtureIngress(t), "database_url": dbURL, "api_key": strings.Repeat("a", 32), "access_secret": strings.Repeat("b", 32),
		"refresh_secret": strings.Repeat("c", 32), "mobile_policy_file": ""}
	parse := func() (*ServerConfig, error) {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		return parseServerDeployment(b)
	}
	cfg, err := parse()
	if err != nil {
		t.Fatal(err)
	}
	db := cfg.protectedDatabase
	if db.ConnConfig.Password != "p@ss" || db.ConnConfig.TLSConfig == nil || db.ConnConfig.TLSConfig.InsecureSkipVerify || len(db.ConnConfig.Fallbacks) != 0 {
		t.Fatal("protected endpoint allowed alternate credentials or transport")
	}
	path := filepath.Join(t.TempDir(), "mobile-secret.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOBILE_POLICY_FILE", path)
	if file, err := cfg.MobilePolicy(); file != nil || err != nil {
		t.Fatal("ambient environment enabled mobile routes")
	}
	p["mobile_policy_file"] = path
	cfg, err = parse()
	if err != nil {
		t.Fatal(err)
	}
	if file, err := cfg.MobilePolicy(); file != nil || err != deployment.ErrConfiguration {
		t.Fatal("worker-writable mobile policy accepted")
	}
	for _, badURL := range []string{strings.Replace(dbURL, "verify-full", "disable", 1), dbURL + "&host=attacker.example", dbURL + "&sslmode=prefer"} {
		p["database_url"] = badURL
		if cfg, err := parse(); cfg != nil || err != deployment.ErrConfiguration {
			t.Fatal("unsafe transport accepted")
		}
	}
	p["database_url"] = dbURL
	p["refresh_secret"] = p["access_secret"]
	if cfg, err := parse(); cfg != nil || err != deployment.ErrConfiguration {
		t.Fatal("cross-purpose signing secrets accepted")
	}
}

func TestProtectedBootstrapRoleSeparation(t *testing.T) {
	clearDeploymentEnv(t)
	data := []byte(`{"database_url":"postgres://setup:one-time@db:5432/hello?sslmode=verify-full","admin_name":"Bootstrap","admin_email":"setup@example.invalid","admin_password":"fixture-password"}`)
	cfg, err := parseBootstrapDeployment(data)
	if err != nil || cfg.protectedDatabase.ConnConfig.RuntimeParams["application_name"] != "hello-bootstrap" {
		t.Fatal("invalid bootstrap policy")
	}
	if c, err := parseServerDeployment(data); c != nil || err != deployment.ErrConfiguration {
		t.Fatal("server accepted bootstrap credentials")
	}
	t.Setenv("PGHOST", "untrusted")
	if c, err := parseBootstrapDeployment(data); c != nil || err != deployment.ErrConfiguration {
		t.Fatal("accepted ambient database override")
	}
}

func clearDeploymentEnv(t *testing.T) {
	t.Helper()
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		if strings.HasPrefix(name, "PG") || name == "SSL_CERT_FILE" || name == "SSL_CERT_DIR" {
			t.Setenv(name, "")
		}
	}
}

func fixtureIngress(t *testing.T) map[string]any {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	return map[string]any{"endpoint": map[string]string{"listen": "127.0.0.1:8080", "client_ca_pem": certPEM, "server_certificate_pem": certPEM, "server_key_pem": keyPEM}, "gateway_ips": []string{"127.0.0.1"}, "ip_per_minute": 120, "auth_ip_per_minute": 10, "auth_total_per_minute": 60, "concurrent_requests": 8}
}
