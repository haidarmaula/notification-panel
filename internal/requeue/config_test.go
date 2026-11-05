package requeue

import (
	"crypto/ed25519"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func validConfiguration(t *testing.T) map[string]any {
	t.Helper()
	key := make([]byte, ed25519.PublicKeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return map[string]any{
		"database_url": "postgresql://replay:secret@db.example.test:5432/notifications?sslmode=verify-full&connect_timeout=5",
		"issuer":       "operations.example.test", "audience": "notification-replay", "deployment": "production-a",
		"verification_key": base64.StdEncoding.EncodeToString(key),
		"log_source_id":    "00000000-0000-4000-8000-000000000003", "cluster_identity": "approved-cluster",
		"topic_name": "notification.send.requested", "topic_incarnation": "reviewed-incarnation", "partition_count": 4, "kafka": validBrokerPolicy(),
	}
}
func configurationJSON(t *testing.T, data map[string]any) string {
	t.Helper()
	value, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}
func clearPostgresEnvironment(t *testing.T) {
	t.Helper()
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		if strings.HasPrefix(name, "PG") || name == "SSL_CERT_FILE" || name == "SSL_CERT_DIR" {
			t.Setenv(name, "")
		}
	}
}

func TestLoadConfigurationPinsTrustAndSource(t *testing.T) {
	clearPostgresEnvironment(t)
	cfg, err := LoadConfiguration(strings.NewReader(configurationJSON(t, validConfiguration(t))))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.database.MaxConns != 2 || cfg.database.MinConns != 0 || cfg.database.ConnConfig.TLSConfig == nil || cfg.database.ConnConfig.TLSConfig.InsecureSkipVerify || cfg.database.ConnConfig.TLSConfig.MinVersion < tls.VersionTLS12 || len(cfg.database.ConnConfig.Fallbacks) != 0 {
		t.Fatal("unsafe database configuration")
	}
	if cfg.verifier == nil || cfg.source.clusterIdentity != "approved-cluster" || cfg.source.topicName != "notification.send.requested" || cfg.source.partitionCount != 4 {
		t.Fatal("trust/source mismatch")
	}
}
func TestLoadConfigurationRejectsUntrustedOrIncompletePolicy(t *testing.T) {
	clearPostgresEnvironment(t)
	tests := []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing trust", func(m map[string]any) { delete(m, "issuer") }},
		{"null trust", func(m map[string]any) { m["audience"] = nil }},
		{"empty deployment", func(m map[string]any) { m["deployment"] = "" }},
		{"unknown override", func(m map[string]any) { m["allow_insecure"] = true }},
		{"actor override", func(m map[string]any) { m["actor_id"] = 1 }},
		{"short key", func(m map[string]any) { m["verification_key"] = "AQID" }},
		{"zero key", func(m map[string]any) { m["verification_key"] = base64.StdEncoding.EncodeToString(make([]byte, 32)) }},
		{"non-string key", func(m map[string]any) { m["verification_key"] = []int{1, 2, 3} }},
		{"plaintext DB", func(m map[string]any) {
			m["database_url"] = "postgresql://replay:secret@db.example.test:5432/notifications?sslmode=disable"
		}},
		{"unverified DB TLS", func(m map[string]any) {
			m["database_url"] = "postgresql://replay:secret@db.example.test:5432/notifications?sslmode=require"
		}},
		{"fallback DB TLS", func(m map[string]any) {
			m["database_url"] = "postgresql://replay:secret@db.example.test:5432/notifications?sslmode=prefer"
		}},
		{"keyword DSN", func(m map[string]any) {
			m["database_url"] = "host=db.example.test user=replay password=secret sslmode=verify-full"
		}},
		{"duplicate SSL policy", func(m map[string]any) {
			m["database_url"] = "postgresql://replay:secret@db.example.test:5432/notifications?sslmode=disable&sslmode=verify-full"
		}},
		{"URI host override", func(m map[string]any) {
			m["database_url"] = "postgresql://replay:secret@db.example.test:5432/notifications?sslmode=verify-full&host=other"
		}},
		{"multi-host", func(m map[string]any) {
			m["database_url"] = "postgresql://replay:secret@one:5432,two:5432/notifications?sslmode=verify-full"
		}},
		{"missing port", func(m map[string]any) {
			m["database_url"] = "postgresql://replay:secret@db.example.test/notifications?sslmode=verify-full"
		}},
		{"missing user", func(m map[string]any) {
			m["database_url"] = "postgresql://db.example.test:5432/notifications?sslmode=verify-full"
		}},
		{"missing password", func(m map[string]any) {
			m["database_url"] = "postgresql://replay@db.example.test:5432/notifications?sslmode=verify-full"
		}},
		{"missing database", func(m map[string]any) {
			m["database_url"] = "postgresql://replay:secret@db.example.test:5432/?sslmode=verify-full"
		}},
		{"slash-only database", func(m map[string]any) {
			m["database_url"] = "postgresql://replay:secret@db.example.test:5432//?sslmode=verify-full"
		}},
		{"encoded-slash database", func(m map[string]any) {
			m["database_url"] = "postgresql://replay:secret@db.example.test:5432/%2F?sslmode=verify-full"
		}},
		{"source zero", func(m map[string]any) { m["log_source_id"] = "00000000-0000-0000-0000-000000000000" }},
		{"missing cluster", func(m map[string]any) { m["cluster_identity"] = "" }},
		{"topic slash", func(m map[string]any) { m["topic_name"] = "not/a/topic" }},
		{"topic dot", func(m map[string]any) { m["topic_name"] = "." }},
		{"missing incarnation", func(m map[string]any) { m["topic_incarnation"] = "" }},
		{"invalid partitions", func(m map[string]any) { m["partition_count"] = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validConfiguration(t)
			tt.change(m)
			cfg, err := LoadConfiguration(strings.NewReader(configurationJSON(t, m)))
			if cfg != nil || !errors.Is(err, ErrConfiguration) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("cfg present=%v err=%v", cfg != nil, err)
			}
		})
	}
}
func TestLoadConfigurationRejectsAmbiguousJSON(t *testing.T) {
	clearPostgresEnvironment(t)
	valid := configurationJSON(t, validConfiguration(t))
	for _, data := range []string{
		valid + " {}",
		strings.Replace(valid, `"issuer":`, `"issuer":"other","issuer":`, 1),
		strings.Replace(valid, `"issuer":`, `"Issuer":`, 1),
		strings.Repeat(" ", 64*1024) + valid,
	} {
		cfg, err := LoadConfiguration(strings.NewReader(data))
		if cfg != nil || !errors.Is(err, ErrConfiguration) {
			t.Fatalf("accepted ambiguous configuration: %v", err)
		}
	}
}

func TestLoadConfigurationRetainsEncodedDatabasePassword(t *testing.T) {
	clearPostgresEnvironment(t)
	data := validConfiguration(t)
	data["database_url"] = "postgresql://replay:p%40ss%3Aword%2Fwith%3Fdelimiters@db.example.test:5432/notifications?sslmode=verify-full"
	cfg, err := LoadConfiguration(strings.NewReader(configurationJSON(t, data)))
	if err != nil || cfg.database.ConnConfig.Password != "p@ss:word/with?delimiters" {
		t.Fatal("encoded credential was rejected or changed")
	}
}

func TestLoadConfigurationRejectsAmbientPostgresSettings(t *testing.T) {
	clearPostgresEnvironment(t)
	for _, name := range []string{"PGDATABASE", "PGOPTIONS", "PGSSLROOTCERT", "PGSERVICE", "PGSERVICEFILE", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "unapproved-setting")
			cfg, err := LoadConfiguration(strings.NewReader(configurationJSON(t, validConfiguration(t))))
			if cfg != nil || !errors.Is(err, ErrConfiguration) {
				t.Fatalf("accepted ambient %s policy", name)
			}
		})
	}
}
