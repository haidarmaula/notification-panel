package daemon

import (
	"testing"

	"hello/internal/deployment"
)

func preparationPolicy() (map[string]any, map[string]any, DeliveryPreparationScope) {
	p := daemonPolicy(deployment.Worker)
	delete(p, "group_id")
	p["mode"], p["topic_name"] = "delivery_only", "delivery-updated"
	d := deliveryPolicy()
	d["environment"] = "staging"
	d["service_account_file"] = DeliveryPreparationCredentialPath
	d["requests_per_second"], d["project_requests_per_second"] = 1, 1
	d["replica_count"], d["concurrency"] = 1, 1
	d["database_pool"] = map[string]any{"max_connections_per_replica": 3, "deployment_connection_budget": 3}
	p["delivery"] = d
	return p, d, DeliveryPreparationScope{
		ApplicationID: d["application_id"].(string), Environment: "staging", DatabaseConnections: 3,
	}
}

func TestDeliveryPreparationPolicyUsesFixedOptionalDatabaseCA(t *testing.T) {
	clearPolicyEnvironment(t)
	p, _, scope := preparationPolicy()
	metadata, err := ValidateDeliveryPreparationPolicy([]byte(policyJSON(t, p)), scope)
	if err != nil || metadata != (DeliveryPreparationMetadata{
		ProjectID: "fixture-project", CredentialProjectID: "fixture-project",
		ServiceAccountEmail: "fixture@fixture-project.iam.gserviceaccount.com", DatabaseUsername: "fixture",
	}) {
		t.Fatal("valid protected preparation policy rejected")
	}
	p["database_url"] = p["database_url"].(string) + "&sslrootcert=" + DeliveryPreparationDBCAPath
	metadata, err = ValidateDeliveryPreparationPolicy([]byte(policyJSON(t, p)), scope)
	if err != nil || !metadata.NeedsDBCA {
		t.Fatal("offline validation did not defer reading the fixed container CA")
	}
}

func TestDeliveryPreparationPolicyRejectsCapabilityAndScopeDrift(t *testing.T) {
	clearPolicyEnvironment(t)
	for _, tc := range []struct {
		name string
		edit func(map[string]any, map[string]any, *DeliveryPreparationScope)
	}{
		{"wrong app", func(_, _ map[string]any, s *DeliveryPreparationScope) {
			s.ApplicationID = "00000000-0000-4000-8000-000000000091"
		}},
		{"wrong environment", func(_, _ map[string]any, s *DeliveryPreparationScope) { s.Environment = "production" }},
		{"wrong budget", func(_, _ map[string]any, s *DeliveryPreparationScope) { s.DatabaseConnections = 2 }},
		{"implicit pool", func(_, d map[string]any, _ *DeliveryPreparationScope) { delete(d, "database_pool") }},
		{"overallocated rate", func(_, d map[string]any, _ *DeliveryPreparationScope) { d["project_requests_per_second"] = 2 }},
		{"extra replica", func(_, d map[string]any, _ *DeliveryPreparationScope) {
			d["replica_count"], d["project_requests_per_second"] = 2, 2
		}},
		{"extra concurrency", func(_, d map[string]any, _ *DeliveryPreparationScope) { d["concurrency"] = 2 }},
		{"unapproved credential path", func(_, d map[string]any, _ *DeliveryPreparationScope) {
			d["service_account_file"] = "/etc/hello/other.json"
		}},
		{"cross-project", func(_, d map[string]any, _ *DeliveryPreparationScope) {
			d["project_id"], d["cross_project_approved"] = "other-project", true
		}},
		{"foreign CA", func(p, _ map[string]any, _ *DeliveryPreparationScope) {
			p["database_url"] = p["database_url"].(string) + "&sslrootcert=/etc/hello/other.crt"
		}},
		{"duplicate CA", func(p, _ map[string]any, _ *DeliveryPreparationScope) {
			p["database_url"] = p["database_url"].(string) + "&sslrootcert=" + DeliveryPreparationDBCAPath + "&sslrootcert="
		}},
		{"DB client key", func(p, _ map[string]any, _ *DeliveryPreparationScope) {
			p["database_url"] = p["database_url"].(string) + "&sslkey=/etc/hello/postgres-key.pem"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, d, scope := preparationPolicy()
			tc.edit(p, d, &scope)
			metadata, err := ValidateDeliveryPreparationPolicy([]byte(policyJSON(t, p)), scope)
			if err != ErrConfiguration || metadata != (DeliveryPreparationMetadata{}) {
				t.Fatal("unsafe preparation accepted or leaked policy metadata")
			}
		})
	}
}
