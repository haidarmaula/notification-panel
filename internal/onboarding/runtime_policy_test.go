package onboarding

import (
	"errors"
	"strings"
	"testing"
)

const validRuntimePolicy = `{"version":1,"environment":"staging","revision":4,"delivery_image":"ghcr.io/hello/delivery@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","network":"hello-staging","max_applications":2,"max_overlap":1,"max_database_connections":9,"delivery":{"memory_bytes":67108864,"nano_cpus":100000000,"pids_limit":16,"max_database_connections":3}}`

func TestParseRuntimePolicyAndBuildScopedPlan(t *testing.T) {
	policy, err := ParseRuntimePolicy([]byte(validRuntimePolicy))
	if err != nil {
		t.Fatalf("ParseRuntimePolicy(): %v", err)
	}
	if err := policy.ValidateCapacity(2, 1); err != nil {
		t.Fatalf("ValidateCapacity(): %v", err)
	}
	plan, err := policy.BuildRuntimePlan("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", EnvironmentStaging, 7)
	if err != nil {
		t.Fatalf("BuildRuntimePlan(): %v", err)
	}
	delivery := plan.Delivery()
	if delivery.ApplicationID != "11111111-1111-4111-8111-111111111111" ||
		delivery.ConfigurationID != "22222222-2222-4222-8222-222222222222" || delivery.Generation != 7 ||
		delivery.PolicyRevision != 4 || delivery.ImageDigest != "ghcr.io/hello/delivery@sha256:"+strings.Repeat("a", 64) ||
		delivery.Replicas != 1 || delivery.Concurrency != 1 || delivery.RequestsPerSecond != 1 || delivery.ProjectRequestsPerSecond != 1 ||
		delivery.UID != 65532 || delivery.GID != 65532 || !delivery.ReadOnlyRoot ||
		!delivery.DropAllCapabilities || !delivery.NoNewPrivileges {
		t.Fatalf("unexpected delivery plan: %#v", delivery)
	}
	acks := plan.RequiredSharedAcknowledgements()
	if len(acks) != 3 || acks[0].Component != "SERVER" || acks[1].Component != "ADMISSION" || acks[2].Component != "SCHEDULER" {
		t.Fatalf("unexpected shared acknowledgements: %#v", acks)
	}
	acks[0].Component = "UNSAFE"
	if plan.RequiredSharedAcknowledgements()[0].Component != "SERVER" {
		t.Fatal("acknowledgement accessor exposed plan storage")
	}
}

func TestParseRuntimePolicyRejectsUnsafeOrNoncanonicalPolicies(t *testing.T) {
	for name, body := range map[string]string{
		"unknown top level":            strings.TrimSuffix(validRuntimePolicy, "}") + `,"command":"sh"}`,
		"unknown nested":               strings.Replace(validRuntimePolicy, `"max_database_connections":3}}`, `"max_database_connections":3,"mount":"/"}}`, 1),
		"alias":                        strings.Replace(validRuntimePolicy, `"network"`, `"Network"`, 1),
		"duplicate":                    strings.Replace(validRuntimePolicy, `"revision":4`, `"revision":4,"revision":5`, 1),
		"null":                         strings.Replace(validRuntimePolicy, `"network":"hello-staging"`, `"network":null`, 1),
		"version":                      strings.Replace(validRuntimePolicy, `"version":1`, `"version":2`, 1),
		"bad digest":                   strings.Replace(validRuntimePolicy, strings.Repeat("a", 64), strings.Repeat("A", 64), 1),
		"image URL":                    strings.Replace(validRuntimePolicy, "ghcr.io/hello/delivery", "https://evil.example/delivery", 1),
		"bad network":                  strings.Replace(validRuntimePolicy, `"network":"hello-staging"`, `"network":"-bad"`, 1),
		"dot network":                  strings.Replace(validRuntimePolicy, `"network":"hello-staging"`, `"network":".."`, 1),
		"invalid image port zero":      strings.Replace(validRuntimePolicy, "ghcr.io/hello/delivery", "ghcr.io:0/hello/delivery", 1),
		"invalid image port overflow":  strings.Replace(validRuntimePolicy, "ghcr.io/hello/delivery", "ghcr.io:65536/hello/delivery", 1),
		"image tag without path":       strings.Replace(validRuntimePolicy, "ghcr.io/hello/delivery", "hello:123", 1),
		"pool budget excludes overlap": strings.Replace(validRuntimePolicy, `"max_database_connections":9`, `"max_database_connections":8`, 1),
		"too large":                    strings.Repeat(" ", runtimePolicyLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRuntimePolicy([]byte(body)); !errors.Is(err, ErrInvalidRuntimePolicy) {
				t.Fatalf("ParseRuntimePolicy() error = %v", err)
			}
		})
	}
}

func TestRuntimePolicyEnforcesCapacityAndPlanIdentity(t *testing.T) {
	policy, err := ParseRuntimePolicy([]byte(validRuntimePolicy))
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.ValidateCapacity(3, 0); !errors.Is(err, ErrRuntimeCapacityExceeded) {
		t.Fatalf("over-capacity result = %v", err)
	}
	if err := policy.ValidateCapacity(2, 2); !errors.Is(err, ErrRuntimeCapacityExceeded) {
		t.Fatalf("overlap result = %v", err)
	}
	if err := policy.ValidateCapacity(-1, 0); !errors.Is(err, ErrRuntimeCapacityExceeded) {
		t.Fatalf("negative capacity result = %v", err)
	}
	for _, tc := range []struct {
		applicationID   string
		configurationID string
		environment     Environment
		generation      int64
	}{
		{applicationID: "11111111111141118111111111111111", configurationID: "22222222-2222-4222-8222-222222222222", environment: EnvironmentStaging, generation: 1},
		{applicationID: "11111111-1111-4111-8111-111111111111", configurationID: "00000000-0000-0000-0000-000000000000", environment: EnvironmentStaging, generation: 1},
		{applicationID: "11111111-1111-4111-8111-111111111111", configurationID: "22222222-2222-4222-8222-222222222222", environment: EnvironmentStaging, generation: 0},
		{applicationID: "11111111-1111-4111-8111-111111111111", configurationID: "22222222-2222-4222-8222-222222222222", environment: EnvironmentProduction, generation: 1},
	} {
		if _, err := policy.BuildRuntimePlan(tc.applicationID, tc.configurationID, tc.environment, tc.generation); !errors.Is(err, ErrInvalidRuntimePlan) {
			t.Fatalf("BuildRuntimePlan(%#v) error = %v", tc, err)
		}
	}
}
