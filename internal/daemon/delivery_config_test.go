package daemon

import (
	"bytes"
	"crypto/ed25519"
	"strings"
	"testing"

	"hello/internal/deployment"
)

func deliveryPolicy() map[string]any {
	return map[string]any{
		"application_id": "00000000-0000-4000-8000-000000000009", "environment": "fixture",
		"project_id": "fixture-project", "service_account_file": "/etc/hello/fixture.json",
		"credential_project_id": "fixture-project", "service_account_email": "fixture@fixture-project.iam.gserviceaccount.com",
		"cross_project_approved": false,
		"routes":                 []map[string]any{{"namespace": "mobile", "key": "android", "platform": "ANDROID", "address_type": "FCM_REGISTRATION_TOKEN"}},
		"lease_seconds":          60, "renew_seconds": 15, "request_timeout_seconds": 10,
		"retry_floor_seconds": 10, "max_attempts": 3, "observation_window_seconds": 7200, "receipt_key_id": "receipt",
		"requests_per_second": 25, "project_requests_per_second": 100, "replica_count": 4, "concurrency": 3,
		"breaker_threshold": 5, "breaker_cooldown_seconds": 15, "poll_interval_ms": 250, "page_size": 20,
		"current_key_version": 1, "fingerprint_key": bytes.Repeat([]byte{1}, 32),
		"encryption_keys": []map[string]any{{"version": 1, "key": bytes.Repeat([]byte{2}, 32)}},
		"signing_keys":    []map[string]any{{"id": "receipt", "key": ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, 32))}},
	}
}

func TestDeliveryPolicyBindsApplicationRouteAndWorkerMode(t *testing.T) {
	clearPolicyEnvironment(t)
	base := daemonPolicy(deployment.Worker)
	delete(base, "group_id")
	base["mode"], base["topic_name"] = "delivery_only", "delivery-updated"
	base["delivery"] = deliveryPolicy()
	cfg, err := Load(deployment.Worker, strings.NewReader(policyJSON(t, base)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.delivery == nil || cfg.delivery.application != cfg.delivery.transport.Routes[0].ApplicationID ||
		cfg.delivery.transport.Routes[0].Environment != cfg.delivery.execution.Receipt.Environment ||
		cfg.delivery.transport.RequestsPerSecond != 25 || cfg.admission.GroupID != "" {
		t.Fatal("delivery configuration failed to pin the selected route, process budget or mode")
	}
	base["mode"] = "admission_only"
	base["group_id"] = "notification-worker"
	if _, err = Load(deployment.Worker, strings.NewReader(policyJSON(t, base))); err != ErrConfiguration {
		t.Fatal("admission mode accepted delivery credentials")
	}
	base["mode"] = "delivery_only"
	if _, err = Load(deployment.Worker, strings.NewReader(policyJSON(t, base))); err != ErrConfiguration {
		t.Fatal("delivery mode accepted a consumer group")
	}
}

func TestDeliveryPolicyRejectsUnsafeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"unallocated request rate", func(p map[string]any) { p["replica_count"] = 5 }},
		{"zero replicas", func(p map[string]any) { p["replica_count"] = 0 }},
		{"late renewal", func(p map[string]any) { p["renew_seconds"] = 21 }},
		{"insufficient write margin", func(p map[string]any) {
			p["lease_seconds"], p["renew_seconds"], p["request_timeout_seconds"] = 30, 10, 15
		}},
		{"wrong receipt key", func(p map[string]any) { p["receipt_key_id"] = "another" }},
		{"signer reuses address encryption key", func(p map[string]any) {
			p["signing_keys"] = []map[string]any{{"id": "receipt", "key": ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32))}}
		}},
		{"incorrect credential pin", func(p map[string]any) { p["service_account_email"] = "wrong@example.org" }},
		{"cross-project not approved", func(p map[string]any) { p["project_id"] = "another-project" }},
		{"untrusted relative file", func(p map[string]any) { p["service_account_file"] = "credential.json" }},
		{"missing current encryption key", func(p map[string]any) { p["current_key_version"] = 2 }},
		{"duplicate key version", func(p map[string]any) {
			keys := p["encryption_keys"].([]map[string]any)
			p["encryption_keys"] = append(keys, keys[0])
		}},
		{"duplicate route", func(p map[string]any) {
			routes := p["routes"].([]map[string]any)
			p["routes"] = append(routes, routes[0])
		}},
		{"route scope override", func(p map[string]any) { p["routes"].([]map[string]any)[0]["application_id"] = p["application_id"] }},
		{"noncanonical app ID", func(p map[string]any) { p["application_id"] = "00000000000040008000000000000009" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := deliveryPolicy()
			tc.edit(input)
			if cfg, err := parseDelivery([]byte(policyJSON(t, input))); cfg != nil || err != ErrConfiguration {
				t.Fatal("accepted unsafe configuration")
			}
		})
	}
}
