package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
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
	"hello/internal/token"
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
	purpose := func(kid, material string) map[string]any {
		return map[string]any{"audience": "aud-" + kid, "active_kid": kid, "keys": []map[string]string{{"kid": kid, "key": base64.RawURLEncoding.EncodeToString([]byte(material))}}}
	}
	trust := map[string]any{"issuer": "protected-staff", "access": purpose("access", strings.Repeat("b", 32)), "refresh": purpose("refresh", strings.Repeat("c", 32))}
	p := map[string]any{"ingress": fixtureIngress(t), "database_url": dbURL, "api_key": strings.Repeat("a", 32), "staff_tokens": trust, "mobile_policy_file": "", "browser_origins": []string{"https://admin.example.test"}}
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
	if len(cfg.CORSAllowedOrigins) != 1 || cfg.CORSAllowedOrigins[0] != "https://admin.example.test" {
		t.Fatalf("protected CORS origins = %#v", cfg.CORSAllowedOrigins)
	}
	delete(p, "browser_origins")
	if cfg, err := parse(); err != nil || len(cfg.CORSAllowedOrigins) != 0 {
		t.Fatal("protected server did not default to denying browser origins")
	}
	p["browser_origins"] = []string{"*"}
	if cfg, err := parse(); cfg != nil || err != deployment.ErrConfiguration {
		t.Fatal("protected server accepted a wildcard browser origin")
	}
	p["browser_origins"] = []string{"https://admin.example.test"}
	p["browser_origins"] = []string{"https://admin.example.test"}
	pair, err := cfg.StaffTokens().Issue(1, "ADMIN", "", token.NewFamily())
	if err != nil {
		t.Fatal(err)
	}
	access, err := cfg.StaffTokens().ParseAccessToken(pair.AccessToken)
	if err != nil || access.Issuer != "protected-staff" || access.Audience[0] != "aud-access" {
		t.Fatal("protected server did not use configured issuer")
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
	delete(p, "ingress")
	if cfg, err := parse(); cfg != nil || err != deployment.ErrConfiguration {
		t.Fatal("protected server accepted a missing mTLS gateway")
	}
	p["ingress"] = fixtureIngress(t)
	p["database_url"] = dbURL
	trust["refresh"] = purpose("refresh", strings.Repeat("b", 32))
	if cfg, err := parse(); cfg != nil || err != deployment.ErrConfiguration {
		t.Fatal("cross-purpose signing secrets accepted")
	}
	trust["refresh"] = purpose("refresh", strings.Repeat("a", 32))
	if cfg, err := parse(); cfg != nil || err != deployment.ErrConfiguration {
		t.Fatal("API key reused for signing")
	}
	delete(p, "staff_tokens")
	p["access_secret"], p["refresh_secret"] = strings.Repeat("b", 32), strings.Repeat("c", 32)
	if cfg, err := parse(); cfg != nil || err != deployment.ErrConfiguration {
		t.Fatal("legacy protected config accepted")
	}
}

func TestLocalServerListenerDefaultsToLoopback(t *testing.T) {
	for name, value := range map[string]string{
		"DB_HOST": "localhost", "DB_PORT": "5432", "DB_USER": "fixture",
		"DB_PASSWORD": "fixture-only", "DB_NAME": "hello", "DB_SSLMODE": "disable",
		"API_KEY": "fixture-api-key", "ACCESS_SECRET": "fixture-access-secret",
		"REFRESH_SECRET": "fixture-refresh-secret", "HTTP_LISTEN_ADDRESS": "",
		"CORS_ALLOWED_ORIGINS": "https://admin.example.test, http://localhost:3000",
	} {
		t.Setenv(name, value)
	}
	cfg := LoadServerConfig()
	if cfg.ListenAddress != "127.0.0.1:8080" {
		t.Fatalf("local listener = %q, want loopback default", cfg.ListenAddress)
	}
	if len(cfg.CORSAllowedOrigins) != 2 || cfg.CORSAllowedOrigins[1] != "http://localhost:3000" {
		t.Fatalf("local CORS origins = %#v", cfg.CORSAllowedOrigins)
	}
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	if cfg := LoadServerConfig(); len(cfg.CORSAllowedOrigins) != 0 {
		t.Fatalf("unset CORS origins = %#v, want default deny", cfg.CORSAllowedOrigins)
	}
	t.Setenv("HTTP_LISTEN_ADDRESS", "0.0.0.0:8080")
	if cfg := LoadServerConfig(); cfg.ListenAddress != "0.0.0.0:8080" {
		t.Fatalf("explicit local listener = %q, want configured address", cfg.ListenAddress)
	}
}

func TestCORSOriginListCanonicalizesAndRejectsUnsafeOrigins(t *testing.T) {
	origins, err := parseCORSOriginList("HTTPS://Admin.Example.Test:443, http://localhost:3000, https://admin.example.test")
	if err != nil {
		t.Fatal("valid origins rejected")
	}
	want := []string{"https://admin.example.test", "http://localhost:3000"}
	if len(origins) != len(want) {
		t.Fatalf("origins = %#v, want %#v", origins, want)
	}
	for i := range want {
		if origins[i] != want[i] {
			t.Fatalf("origin[%d] = %q, want %q", i, origins[i], want[i])
		}
	}

	for _, value := range []string{"*", "https://*", "null", "https://admin.example.test/", "ftp://admin.example.test", "https://user@admin.example.test", "https://admin.example.test?x=1", "https://admin.example.test,"} {
		if _, err := parseCORSOriginList(value); err == nil {
			t.Errorf("unsafe origin %q accepted", value)
		}
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
