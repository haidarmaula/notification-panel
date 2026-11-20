package main

import (
	"strings"
	"testing"
)

func TestReleaseReviewPolicyBindsExplicitEvidenceAndIdentity(t *testing.T) {
	const scope = `"database_url":"postgres://reviewer@example.invalid/hello","application_id":"10000000-0000-4000-8000-000000000001","environment":"staging","expires_at":"2026-11-20T00:00:00Z"`
	const evidence = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	approval := `{` + scope + `,"approval_id":"10000000-0000-4000-8000-000000000002","mode":"PILOT","configuration_id":"10000000-0000-4000-8000-000000000003","cohort_id":"10000000-0000-4000-8000-000000000004","evidence_sha256":"` + evidence + `"}`
	delegation := `{` + scope + `,"grant_id":"10000000-0000-4000-8000-000000000005","operation_id":"10000000-0000-4000-8000-000000000006","runtime_generation":2,"legacy_sha256":"` + evidence + `","consent_sha256":"` + evidence + `","coverage_sha256":"` + evidence + `","quota_sha256":"` + evidence + `"}`
	for _, tc := range []struct {
		mode, body, function string
		args                 int
	}{
		{"approve-release", approval, "approve_onboarding_release", 8},
		{"delegate-release", delegation, "delegate_onboarding_release", 10},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			policy, err := parseReleaseReviewPolicy(tc.mode, []byte(tc.body))
			if err != nil || !strings.Contains(policy.query, tc.function) || len(policy.args) != tc.args {
				t.Fatal("valid protected release request was not accepted")
			}
			for _, bad := range []string{
				strings.Replace(tc.body, evidence, "123", 1),
				strings.Replace(tc.body, evidence, strings.ToUpper(evidence), 1),
				strings.Replace(tc.body, "staging", "default", 1),
				strings.Replace(tc.body, "2026-11-20T00:00:00Z", "tomorrow", 1),
				strings.TrimSuffix(tc.body, "}") + `,"executor_role":"fixture_dispatch_operator"}`,
			} {
				if _, err := parseReleaseReviewPolicy(tc.mode, []byte(bad)); err == nil {
					t.Fatal("invalid protected release request accepted")
				}
			}
		})
	}
}
