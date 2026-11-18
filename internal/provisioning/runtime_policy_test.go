package provisioning

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"hello/internal/deployment"
)

func TestDeliveryPolicyPathBindsApplicationConfigurationAndGeneration(t *testing.T) {
	path, err := DeliveryPolicyPath(testAppID, "staging", testConfigID, 45)
	want := "/var/lib/hello/runtime/" + testAppID + "/staging/" + testConfigID + "/45/worker.json"
	if err != nil || path != want {
		t.Fatalf("unexpected path %q: %v", path, err)
	}
	for _, input := range []struct {
		app, environment, config string
		generation               int64
	}{
		{app: "../" + testAppID, environment: "staging", config: testConfigID, generation: 1},
		{app: testAppID, environment: "../production", config: testConfigID, generation: 1},
		{app: testAppID, environment: "staging", config: "00000000-0000-0000-0000-000000000000", generation: 1},
		{app: testAppID, environment: "staging", config: testConfigID, generation: 0},
	} {
		if path, err := DeliveryPolicyPath(input.app, input.environment, input.config, input.generation); err == nil || path != "" {
			t.Fatalf("invalid scope produced path %q", path)
		}
	}
}

func TestPublishDeliveryPolicyPreservesExactGeneration(t *testing.T) {
	_, root := rootOwnedStore(t)
	ctx := context.Background()
	original := []byte("{\"worker_mode\":\"delivery\"}\n")
	path, err := publishDeliveryPolicy(ctx, root, testAppID, "staging", testConfigID, 1, original)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := publishDeliveryPolicy(ctx, root, testAppID, "staging", testConfigID, 1, original); err != nil || replay != path {
		t.Fatal("exact replay failed")
	}
	changed := bytes.TrimSpace(original)
	if _, err := publishDeliveryPolicy(ctx, root, testAppID, "staging", testConfigID, 1, changed); err == nil {
		t.Fatal("replay replaced immutable bytes")
	}
	if next, err := publishDeliveryPolicy(ctx, root, testAppID, "staging", testConfigID, 2, changed); err != nil || next == path {
		t.Fatal("next generation could not publish independently")
	}
	data, err := deployment.ReadSecret(path)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("first generation is no longer readable with exact bytes through the deployment reader")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := publishDeliveryPolicy(ctx, root, testAppID, "staging", testConfigID, 1, original); err == nil {
		t.Fatal("replay accepted an unsafe mode")
	}
}

func TestPublishDeliveryPolicyRejectsSymlinkWithoutChangingTarget(t *testing.T) {
	_, root := rootOwnedStore(t)
	ctx := context.Background()
	target, err := publishDeliveryPolicy(ctx, root, testAppID, "staging", testConfigID, 1, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	otherGeneration := filepath.Join(filepath.Dir(filepath.Dir(target)), "2")
	if err := os.Symlink(filepath.Dir(target), otherGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := publishDeliveryPolicy(ctx, root, testAppID, "staging", testConfigID, 2, []byte("{}")); err == nil {
		t.Fatal("publication followed a symlinked generation")
	}
	if data, err := deployment.ReadSecret(target); err != nil || !bytes.Equal(data, []byte("{}")) {
		t.Fatal("publication changed the original generation")
	}
}
