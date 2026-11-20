package main

import (
	"strings"
	"testing"
)

func TestFirstActivationApprovalBindsExplicitEvidence(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	data := `{"database_url":"postgres://issuer@db","application_id":"10000000-0000-4000-8000-000000000001",
	"operation_id":"10000000-0000-4000-8000-000000000002","grant_id":"10000000-0000-4000-8000-000000000003",
	"executor":"activation_operator","generation":1,"retired_writer_roles":[],"ownership_sha256":"` + digest +
		`","writer_fence_sha256":"` + digest + `","cohort_fence_sha256":"` + digest + `"}`
	got, err := parseFirstActivationApproval([]byte(data))
	if err != nil || got.generation != 1 || got.executor != "activation_operator" ||
		len(got.ownership) != 32 || !got.project.Valid || !got.operation.Valid || !got.grant.Valid || got.retired == nil {
		t.Fatalf("valid explicit approval rejected: %v", err)
	}
	for name, invalid := range map[string]string{
		"duplicate generation": strings.Replace(data, `"generation":1`, `"generation":1,"generation":2`, 1),
		"invalid executor":     strings.Replace(data, "activation_operator", "operator;select", 1),
		"invalid retired role": strings.Replace(data, "[]", `[""]`, 1),
		"missing evidence":     strings.Replace(data, `,"cohort_fence_sha256":"`+digest+`"`, "", 1),
		"uppercase evidence":   strings.Replace(data, digest, strings.ToUpper(digest), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseFirstActivationApproval([]byte(invalid)); err == nil {
				t.Fatal("invalid approval was accepted")
			}
		})
	}
}
