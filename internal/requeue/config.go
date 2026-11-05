package requeue

import (
	"bytes"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"hello/internal/dispatch"
	"hello/internal/kafka"
)

// ErrConfiguration deliberately omits decoder, DSN, key and filesystem details.
var ErrConfiguration = errors.New("invalid replay deployment configuration")

// Configuration retains read-once deployment policy for future session wiring.
// Fields remain private: callers cannot change routing or trust after loading.
// Loading is not approval of a source incarnation and opens no network connection.
type Configuration struct {
	database *pgxpool.Config
	verifier *dispatch.ReplayVerifier
	source   sourceManifest
	kafka    kafka.ScopedProducerConfig
}

type sourceManifest struct {
	id               pgtype.UUID
	clusterIdentity  string
	topicName        string
	topicIncarnation string
	partitionCount   int32
}

// LoadConfiguration reads an exact-schema policy from a caller-owned trusted
// stream. The caller must protect the file and its parent directories against
// unauthorized modification. It does not load .env, activate a source or accept
// an insecure transport override. Broker TLS/SASL and live source verification
// remain separate prerequisites before a session factory may enable execution.
func LoadConfiguration(source io.Reader) (*Configuration, error) {
	if source == nil || !configurationEnvironmentAllowed() {
		return nil, ErrConfiguration
	}
	data, err := io.ReadAll(io.LimitReader(source, 64*1024+1))
	if err != nil || len(data) > 64*1024 || !utf8.Valid(data) {
		return nil, ErrConfiguration
	}
	var databaseURL, issuer, audience, deployment, keyText, sourceID string
	var manifest sourceManifest
	var kafkaPolicy json.RawMessage
	if decodeObject(data, map[string]any{
		"database_url": &databaseURL, "issuer": &issuer, "audience": &audience, "deployment": &deployment,
		"verification_key": &keyText, "log_source_id": &sourceID, "cluster_identity": &manifest.clusterIdentity,
		"topic_name": &manifest.topicName, "topic_incarnation": &manifest.topicIncarnation, "partition_count": &manifest.partitionCount, "kafka": &kafkaPolicy,
	}) != nil {
		return nil, ErrConfiguration
	}
	key, err := base64.StdEncoding.Strict().DecodeString(keyText)
	if err != nil || len(key) != ed25519.PublicKeySize || bytes.Equal(key, make([]byte, ed25519.PublicKeySize)) || base64.StdEncoding.EncodeToString(key) != keyText {
		return nil, ErrConfiguration
	}
	verifier, err := dispatch.NewReplayVerifier(issuer, audience, deployment, key)
	if err != nil {
		return nil, ErrConfiguration
	}
	manifest.id, err = parseUUID(sourceID)
	if err != nil || !validPolicyText(manifest.clusterIdentity) || !validPolicyText(manifest.topicIncarnation) ||
		!validTopic(manifest.topicName) || manifest.partitionCount < 1 {
		return nil, ErrConfiguration
	}
	database, err := parseDatabasePolicy(databaseURL)
	if err != nil {
		return nil, ErrConfiguration
	}
	brokerConfig, err := parseBrokerPolicy(kafkaPolicy, manifest.topicName)
	if err != nil {
		return nil, ErrConfiguration
	}
	return &Configuration{database: database, verifier: verifier, source: manifest, kafka: brokerConfig}, nil
}

func validPolicyText(value string) bool {
	return len(value) > 0 && len(value) <= 255 && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

func validTopic(value string) bool {
	if len(value) < 1 || len(value) > 249 || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// parseDatabasePolicy requires explicit endpoint and identity to avoid accidental
// environment defaults. Only verified TLS is permitted; host/identity overrides,
// multiple hosts, duplicate query fields and plaintext fallbacks are rejected.
func parseDatabasePolicy(value string) (*pgxpool.Config, error) {
	if !configurationEnvironmentAllowed() {
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
	cfg.ConnConfig.RuntimeParams["application_name"] = "hello-requeue"
	return cfg, nil
}

// Reject alternate policy/trust sources before pgx parsing or deferred TLS work.
// The loader never clears or rewrites the process environment.
func configurationEnvironmentAllowed() bool {
	for _, setting := range os.Environ() {
		name, value, _ := strings.Cut(setting, "=")
		if value != "" && (strings.HasPrefix(name, "PG") || name == "SSL_CERT_FILE" || name == "SSL_CERT_DIR") {
			return false
		}
	}
	return true
}
