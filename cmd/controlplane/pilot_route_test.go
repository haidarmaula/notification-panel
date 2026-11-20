package main

import (
	"strings"
	"testing"
)

func TestPilotRoutePolicyKeepsApprovalAndApplicationSeparate(t *testing.T) {
	const base = `{"database_url":"postgres://route@example.invalid/hello","application_id":"10000000-0000-4000-8000-000000000001","environment":"staging","grant_id":"10000000-0000-4000-8000-000000000002"`
	const approvalFields = `,"cohort_id":"10000000-0000-4000-8000-000000000003","installation_id":"10000000-0000-4000-8000-000000000004","expires_at":"2026-11-20T00:00:00Z"`
	approval, err := parsePilotRoutePolicy("approve-pilot-route", []byte(base+approvalFields+"}"))
	if err != nil || !approval.issue || !validPilotRouteResult(approval, approval.grant.String()) {
		t.Fatal("valid route approval is unavailable")
	}
	apply, err := parsePilotRoutePolicy("apply-pilot-route", []byte(base+"}"))
	if err != nil || apply.issue || !validPilotRouteResult(apply, "1") || validPilotRouteResult(apply, approval.grant.String()) {
		t.Fatal("valid route execution is unavailable")
	}
	for _, body := range []string{
		base + approvalFields + "}",
		base + `,"destination":"FULL"}`,
		strings.Replace(base, "staging", "unknown", 1) + "}",
	} {
		if _, err := parsePilotRoutePolicy("apply-pilot-route", []byte(body)); err == nil {
			t.Fatal("route execution accepted malformed or approval-only input")
		}
	}
}
