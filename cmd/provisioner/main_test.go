package main

import "testing"

func TestRejectsPolicyPathOverride(t *testing.T) {
	if code := run([]string{"--policy", "/untrusted/config.json"}); code != 2 {
		t.Fatalf("unexpected exit code: %d", code)
	}
}
