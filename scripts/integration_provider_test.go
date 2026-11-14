//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalProviderRequiresExplicitModeAndSyntheticDestination(t *testing.T) {
	for _, tc := range []struct {
		token             string
		local, load, want bool
	}{
		{"load-1", true, false, true}, {"load-3", true, false, true}, {"load-4", true, false, false},
		{"real-device", true, false, false}, {"load-1", false, false, false}, {"load-1", false, true, false},
	} {
		t.Run(fmt.Sprintf("%s/local=%t/load=%t", tc.token, tc.local, tc.load), func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"message": map[string]any{"token": tc.token, "android": map[string]string{"ttl": "3600s"}, "data": map[string]string{
				"schema_version": "1", "application_id": "fixture-app", "notification_id": "1", "delivery_id": "fixture-delivery", "registration_id": "1", "receipt_token": "r1.fixture-key.synthetic",
			}}})
			r := httptest.NewRequest("POST", "https://fcm.googleapis.com/v1/projects/integration-fixture/messages:send", strings.NewReader(string(raw)))
			r.Header.Set("Authorization", "Bearer "+fixtureBearer)
			_, _, _, valid := readSend(r, tc.load, tc.local)
			if valid != tc.want {
				t.Fatalf("accepted=%v, want=%v", valid, tc.want)
			}
		})
	}
}
