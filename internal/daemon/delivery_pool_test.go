package daemon

import (
	"strings"
	"testing"

	"hello/internal/deployment"
)

func TestDeliveryDatabasePoolAllocation(t *testing.T) {
	clearPolicyEnvironment(t)
	for _, tc := range []struct {
		name       string
		allocation any
		want       int32
	}{
		{"existing policy", nil, 2},
		{"allocated to four replicas", map[string]int{"max_connections_per_replica": 8, "deployment_connection_budget": 32}, 8},
		{"insufficient total budget", map[string]int{"max_connections_per_replica": 8, "deployment_connection_budget": 31}, 0},
		{"no budget", map[string]int{"max_connections_per_replica": 8}, 0},
		{"one connection", map[string]int{"max_connections_per_replica": 1, "deployment_connection_budget": 32}, 0},
		{"per-process ceiling", map[string]int{"max_connections_per_replica": 33, "deployment_connection_budget": 256}, 0},
		{"negative budget", map[string]int{"max_connections_per_replica": 8, "deployment_connection_budget": -32}, 0},
		{"unrecognized field", map[string]int{"max_connections_per_replica": 8, "deployment_connection_budget": 32, "minimum": 1}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := daemonPolicy(deployment.Worker)
			delete(base, "group_id")
			delivery := deliveryPolicy()
			if tc.allocation != nil {
				delivery["database_pool"] = tc.allocation
			}
			base["mode"], base["topic_name"], base["delivery"] = "delivery_only", "delivery-updated", delivery
			cfg, err := Load(deployment.Worker, strings.NewReader(policyJSON(t, base)))
			if tc.want == 0 {
				if cfg != nil || err != ErrConfiguration {
					t.Fatal("unsafe database allocation accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.database.MaxConns != tc.want || cfg.database.MinConns != 0 {
				t.Fatalf("pool min/max = %d/%d, want 0/%d", cfg.database.MinConns, cfg.database.MaxConns, tc.want)
			}
		})
	}
}
