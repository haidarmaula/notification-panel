package onboarding

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This opt-in fixture uses only a locally cached digest; it never starts a
// container. The fake guard isolates Docker behavior from the separate SQL-role
// fixture and does not establish database authority or activation readiness.
func TestRuntimePrepareDocker(t *testing.T) {
	image := os.Getenv("HELLO_ONBOARDING_DOCKER_IMAGE")
	if image == "" {
		t.Skip("run scripts/application-onboarding-docker.sh")
	}
	policy, err := ParseRuntimePolicy([]byte(strings.Replace(validRuntimePolicy, "ghcr.io/hello/delivery@sha256:"+strings.Repeat("a", 64), image, 1)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p := NewRuntimePreparer(&preparationGuard{})
	defer p.Close()
	plan, err := policy.BuildRuntimePlan(uuid.NewString(), uuid.NewString(), EnvironmentStaging, 1)
	if err != nil {
		t.Fatal(err)
	}
	name, err := p.Prepare(ctx, plan)
	// Prepare returns its deterministic name even on an ambiguous Engine response.
	// Cleanup is fixture-only and independently checks immutable application scope.
	if name != "" {
		t.Cleanup(func() {
			p.client.Timeout = 30 * time.Second
			cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			response, cleanupErr := p.request(cleanupCtx, "GET", name+"/json", nil)
			if cleanupErr != nil {
				t.Errorf("inspect cleanup %s: %v", name, cleanupErr)
				return
			}
			defer response.Body.Close()
			if response.StatusCode == 404 {
				return
			}
			var actual struct {
				Config struct{ Labels map[string]string }
				State  struct{ Running bool }
			}
			if json.NewDecoder(response.Body).Decode(&actual) != nil || actual.State.Running || actual.Config.Labels["hello.onboarding.application"] != plan.Delivery().ApplicationID {
				t.Errorf("refusing cleanup of unexpected allocation %s", name)
				return
			}
			removed, removeErr := p.request(cleanupCtx, "DELETE", name+"?v=true", nil)
			if removeErr != nil {
				t.Errorf("remove cleanup %s: %v", name, removeErr)
				return
			}
			defer removed.Body.Close()
			if removed.StatusCode != 204 {
				t.Errorf("remove cleanup %s: status %d", name, removed.StatusCode)
			}
		})
	}
	if err != nil {
		t.Fatalf("Prepare(%s): %v", name, err)
	}
	replayed, err := p.Prepare(ctx, plan)
	if err != nil || replayed != name {
		t.Fatalf("replay: %s %v", replayed, err)
	}
}
