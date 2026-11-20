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
			_, _, _, valid := readSend(r, tc.load, tc.local, false)
			if valid != tc.want {
				t.Fatalf("accepted=%v, want=%v", valid, tc.want)
			}
		})
	}
}

func TestOnboardingProviderDistinguishesDryRunFromDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, authorization, payload string
		dryRun, valid                bool
	}{
		{"approved token", "Bearer " + fixtureBearer, `{"validate_only":true,"message":{"token":"execution-address-0"}}`, true, true},
		{"wrong bearer", "Bearer unknown", `{"validate_only":true,"message":{"token":"execution-address-0"}}`, true, false},
		{"wrong token", "Bearer " + fixtureBearer, `{"validate_only":true,"message":{"token":"some-other-token"}}`, true, false},
		{"extra data", "Bearer " + fixtureBearer, `{"validate_only":true,"message":{"token":"execution-address-0","data":{"hello":"world"}}}`, true, false},
		{"trailing object", "Bearer " + fixtureBearer, `{"validate_only":true,"message":{"token":"execution-address-0"}}{}`, true, false},
		{"ordinary send", "Bearer " + fixtureBearer, `{"message":{"token":"execution-address-0","data":{"fixture":"integration"}}}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dryRun, valid := readOnboardingDryRun(tc.authorization, []byte(tc.payload))
			if dryRun != tc.dryRun || valid != tc.valid {
				t.Fatalf("dry run, valid = %v, %v; want %v, %v", dryRun, valid, tc.dryRun, tc.valid)
			}
		})
	}
}

func TestOnboardingProviderAcceptsOnlyTheSyntheticAPICampaign(t *testing.T) {
	for _, tc := range []struct {
		name, token, title, body, ttl string
		onboarding, want              bool
	}{
		{"captured API campaign", "execution-address-0", "Fixture pilot", "Synthetic enrollment test", "120s", true, true},
		{"ordinary integration mode", "execution-address-0", "Fixture pilot", "Synthetic enrollment test", "120s", false, false},
		{"different text", "execution-address-0", "Fixture pilot", "Arbitrary content", "120s", true, false},
		{"nonfixture token", "real-device", "Fixture pilot", "Synthetic enrollment test", "120s", true, false},
		{"excess TTL", "execution-address-0", "Fixture pilot", "Synthetic enrollment test", "301s", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"message": map[string]any{
				"token":        tc.token,
				"android":      map[string]string{"ttl": tc.ttl},
				"notification": map[string]string{"title": tc.title, "body": tc.body},
				"data": map[string]string{
					"schema_version": "1", "application_id": "fixture-app", "notification_id": "1",
					"delivery_id": "fixture-delivery", "registration_id": "1", "receipt_token": "r1.fixture-key.synthetic",
				},
			}})
			r := httptest.NewRequest("POST", "/", strings.NewReader(string(raw)))
			r.Header.Set("Authorization", "Bearer "+fixtureBearer)
			_, _, _, valid := readSend(r, false, false, tc.onboarding)
			if valid != tc.want {
				t.Fatalf("accepted=%v, want=%v", valid, tc.want)
			}
		})
	}
}
