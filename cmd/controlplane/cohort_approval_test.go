package main

import (
	"strings"
	"testing"
)

const validCohortApproval = `{
	"database_url":"postgresql://restricted:fixture@pg:5432/local?sslmode=verify-full",
	"application_id":"11111111-1111-4111-8111-111111111111",
	"environment":"staging",
	"approval_id":"22222222-2222-4222-8222-222222222222",
	"configuration_id":"33333333-3333-4333-8333-333333333333",
	"installation_ids":["44444444-4444-4444-8444-444444444444"],
	"issuer_key_id":"test-consent",
	"evidence_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	"expires_at":"2026-11-19T00:00:00Z"
}`

func TestCohortApprovalRequiresExactReviewedRosterAndDigest(t *testing.T) {
	scope, err := parseCohortApproval([]byte(validCohortApproval))
	if err != nil || len(scope.installations) != 1 || len(scope.evidence) != 32 {
		t.Fatalf("valid approval rejected: %v", err)
	}
	for _, mutation := range [][2]string{
		{`"installation_ids":[`, `"installation_ids":["44444444-4444-4444-8444-444444444444",`},
		{`"staging"`, `"local"`},
		{`0123456789abcdef`, `0123456789ABCDEF`},
		{`"issuer_key_id"`, `"unrecognised"`},
		{`2026-11-19T00:00:00Z`, `tomorrow`},
	} {
		if _, err := parseCohortApproval([]byte(strings.Replace(validCohortApproval, mutation[0], mutation[1], 1))); err == nil {
			t.Fatalf("accepted malformed policy for %s", mutation[0])
		}
	}
}

func TestCoordinatorPolicyCanDiscoverWorkWithoutOperationID(t *testing.T) {
	const policy = `{"application_id":"11111111-1111-4111-8111-111111111111","environment":"staging","database_url":"protected"}`
	_, scope, err := parseCoordinator([]byte(policy))
	if err != nil || scope.Operation.Valid || !scope.Project.Valid {
		t.Fatalf("discovery policy was rejected: %v", err)
	}
	invalid := strings.TrimSuffix(policy, "}") + `,"operation_id":null}`
	if _, _, err := parseCoordinator([]byte(invalid)); err == nil {
		t.Fatal("accepted explicit null operation_id")
	}
}
