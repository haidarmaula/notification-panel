package main

import (
	"errors"
	"strings"
	"testing"
)

func TestFirstActivationPolicy(t *testing.T) {
	const valid = `{"database_url":"fixture","operation_id":"11111111-1111-4111-8111-111111111111","grant_id":"22222222-2222-4222-8222-222222222222","instance_id":"abcdabcd-abcd-4bcd-8bcd-abcdabcdabcd"}`
	policy, err := parseFirstActivationPolicy([]byte(valid))
	if err != nil || policy.databaseURL != "fixture" || policy.grant.String() != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("expected a valid activation policy: %v", err)
	}
	for name, input := range map[string]string{
		"extra setting":      strings.Replace(valid, `{`, `{"application_id":"unapproved",`, 1),
		"duplicate setting":  strings.Replace(valid, `{`, `{"database_url":"other",`, 1),
		"null operation":     strings.Replace(valid, `"11111111-1111-4111-8111-111111111111"`, `null`, 1),
		"nil grant":          strings.Replace(valid, "22222222-2222-4222-8222-222222222222", "00000000-0000-0000-0000-000000000000", 1),
		"uppercase instance": strings.Replace(valid, "abcdabcd", "ABCDABCD", 1),
		"trailing object":    valid + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseFirstActivationPolicy([]byte(input)); !errors.Is(err, errActivationPolicy) {
				t.Fatalf("expected policy rejection, got %v", err)
			}
		})
	}
}
