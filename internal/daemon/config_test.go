package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"hello/internal/deployment"
)

func daemonPolicy(role deployment.Component) map[string]any {
	p := map[string]any{"database_url": "postgresql://fixture:fixture_only@db.example.test:5432/notifications?sslmode=verify-full", "log_source_id": "00000000-0000-4000-8000-000000000003", "cluster_identity": "cluster", "topic_name": "send", "topic_incarnation": "reviewed", "partition_count": 2, "retry_min_ms": 100, "retry_max_ms": 1000,
		"kafka": map[string]any{"brokers": []string{"broker.example.test:9093"}, "authentication": "SASL_PLAIN", "ca_pem": "", "client_certificate_pem": "", "client_key_pem": "", "sasl_username": "fixture", "sasl_password": "fixture_only"}}
	if role == deployment.Worker {
		p["mode"] = "admission_only"
		p["group_id"] = "notification-worker"
	} else {
		p["poll_interval_ms"] = 100
		p["page_size"] = 10
	}
	return p
}
func policyJSON(t *testing.T, p map[string]any) string {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func clearPolicyEnvironment(t *testing.T) {
	t.Helper()
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		if strings.HasPrefix(name, "PG") || name == "SSL_CERT_FILE" || name == "SSL_CERT_DIR" {
			t.Setenv(name, "")
		}
	}
}

func TestLoadDaemonPoliciesWithoutReplayAuthority(t *testing.T) {
	clearPolicyEnvironment(t)
	for _, role := range []deployment.Component{deployment.Worker, deployment.Scheduler} {
		t.Run(string(role), func(t *testing.T) {
			cfg, err := Load(role, strings.NewReader(policyJSON(t, daemonPolicy(role))))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.role != role || cfg.database.ConnConfig.RuntimeParams["application_name"] != "hello-"+string(role) {
				t.Fatal("incorrect role binding")
			}
		})
	}
}
func TestLoadRejectsRoleConfusionAndUnsafeOptions(t *testing.T) {
	clearPolicyEnvironment(t)
	for _, tt := range []struct {
		name   string
		role   deployment.Component
		change func(map[string]any)
	}{
		{"worker delivery mode not implemented", deployment.Worker, func(p map[string]any) { p["mode"] = "delivery" }},
		{"worker group invalid", deployment.Worker, func(p map[string]any) { p["group_id"] = "forged\ngroup" }},
		{"worker cannot configure scheduler", deployment.Worker, func(p map[string]any) { p["page_size"] = 10 }},
		{"scheduler cannot configure consumer", deployment.Scheduler, func(p map[string]any) { p["group_id"] = "group" }},
		{"no operator key in daemon", deployment.Worker, func(p map[string]any) { p["verification_key"] = "untrusted" }},
		{"no config override", deployment.Scheduler, func(p map[string]any) { p["config_path"] = "/tmp/policy" }},
		{"unbounded poll", deployment.Scheduler, func(p map[string]any) { p["poll_interval_ms"] = 0 }},
		{"overflow poll", deployment.Scheduler, func(p map[string]any) { p["poll_interval_ms"] = int64(1 << 62) }},
		{"unbounded page", deployment.Scheduler, func(p map[string]any) { p["page_size"] = 101 }},
		{"busy retry", deployment.Worker, func(p map[string]any) { p["retry_min_ms"] = 0 }},
		{"inverted retry", deployment.Worker, func(p map[string]any) { p["retry_max_ms"] = 1 }},
		{"insecure DB", deployment.Worker, func(p map[string]any) {
			p["database_url"] = "postgresql://fixture:fixture_only@db:5432/notifications?sslmode=disable"
		}},
		{"source UUID alias", deployment.Worker, func(p map[string]any) { p["log_source_id"] = "00000000000040008000000000000003" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := daemonPolicy(tt.role)
			tt.change(p)
			cfg, err := Load(tt.role, strings.NewReader(policyJSON(t, p)))
			if cfg != nil || !errors.Is(err, ErrConfiguration) {
				t.Fatalf("accepted unsafe policy: %v", err)
			}
		})
	}
	if cfg, err := Load(deployment.Requeue, strings.NewReader("{}")); cfg != nil || !errors.Is(err, ErrConfiguration) {
		t.Fatal("daemon accepted replay role")
	}
}
func TestLoadRejectsAmbiguousAndIncompleteJSON(t *testing.T) {
	clearPolicyEnvironment(t)
	valid := policyJSON(t, daemonPolicy(deployment.Worker))
	for _, input := range []string{valid + "{}", strings.Replace(valid, `"mode":`, `"mode":"other","mode":`, 1), strings.Replace(valid, `"mode":`, `"Mode":`, 1), strings.Replace(valid, `"mode":"admission_only"`, `"mode":null`, 1), strings.Repeat(" ", 64*1024) + valid} {
		if cfg, err := Load(deployment.Worker, strings.NewReader(input)); cfg != nil || !errors.Is(err, ErrConfiguration) {
			t.Fatal("accepted ambiguous input")
		}
	}
}
