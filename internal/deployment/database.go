package deployment

import (
	"crypto/tls"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Database requires explicit endpoint and identity to avoid accidental
// environment defaults. Only verified TLS is permitted; host/identity overrides,
// multiple hosts, duplicate query fields and plaintext fallbacks are rejected.
func Database(value, applicationName string) (*pgxpool.Config, error) {
	if !EnvironmentAllowed() || !validPolicyText(applicationName) || len(applicationName) > 63 {
		return nil, ErrConfiguration
	}

	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.User == nil ||
		u.User.Username() == "" || u.Port() == "" || strings.TrimLeft(u.Path, "/") == "" || u.Fragment != "" || strings.Contains(u.Host, ",") {
		return nil, ErrConfiguration
	}
	password, ok := u.User.Password()
	if !ok || password == "" {
		return nil, ErrConfiguration
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || port == 0 {
		return nil, ErrConfiguration
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || query.Get("sslmode") != "verify-full" {
		return nil, ErrConfiguration
	}
	for name, values := range query {
		if len(values) != 1 {
			return nil, ErrConfiguration
		}
		switch name {
		case "sslmode", "sslrootcert", "sslcert", "sslkey", "connect_timeout":
		default:
			return nil, ErrConfiguration
		}
	}
	// Override pgx's implicit home-directory certificate and passfile defaults.
	// An empty root path explicitly uses system roots with hostname verification.
	for _, name := range []string{"sslrootcert", "sslcert", "sslkey"} {
		query.Set(name, query.Get(name))
	}
	query.Set("passfile", os.DevNull)
	u.RawQuery = query.Encode()
	cfg, err := pgxpool.ParseConfig(u.String())
	if err != nil || cfg.ConnConfig.TLSConfig == nil || cfg.ConnConfig.TLSConfig.InsecureSkipVerify || len(cfg.ConnConfig.Fallbacks) != 0 || cfg.ConnConfig.Database != strings.TrimLeft(u.Path, "/") {
		return nil, ErrConfiguration
	}
	cfg.ConnConfig.TLSConfig.MinVersion = tls.VersionTLS12
	cfg.MaxConns = 2
	cfg.MinConns = 0
	cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	return cfg, nil
}

// EnvironmentAllowed rejects alternate policy/trust sources before pgx parsing or deferred TLS work.
// The loader never clears or rewrites the process environment.
func EnvironmentAllowed() bool {
	for _, setting := range os.Environ() {
		name, value, _ := strings.Cut(setting, "=")
		if value != "" && (strings.HasPrefix(name, "PG") || name == "SSL_CERT_FILE" || name == "SSL_CERT_DIR") {
			return false
		}
	}
	return true
}
