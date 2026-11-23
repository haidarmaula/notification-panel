//go:build ignore

package main

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func routeCohort(value string) pgtype.UUID {
	var result pgtype.UUID
	if err := result.Scan(value); err != nil {
		panic(err)
	}
	return result
}

func TestPlanRealFCMSelectionSwitchesOnlySelectedRoute(t *testing.T) {
	oldCohort := routeCohort("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	bindings := []realFCMBinding{
		{id: 10, installationID: "install-a", destination: "FCM_ACTIVE", cohort: oldCohort, revision: 1},
		{id: 11, installationID: "install-b", destination: "", revision: 0},
	}

	cohort, mutations, err := planRealFCMSelection(bindings, "install-b")
	if err != nil {
		t.Fatalf("planRealFCMSelection() error = %v", err)
	}
	if cohort == uuid.Nil || cohort == uuid.UUID(oldCohort.Bytes) {
		t.Fatalf("new selection cohort = %s, want a fresh cohort", cohort)
	}
	if len(mutations) != 2 || mutations[0].binding.installationID != "install-a" || mutations[0].destination != "HELD" ||
		mutations[1].binding.installationID != "install-b" || mutations[1].destination != "FCM_ACTIVE" {
		t.Fatalf("route mutations = %#v, want old route held and selected route active", mutations)
	}
	if len(bindings) != 2 || bindings[0].destination != "FCM_ACTIVE" || bindings[1].destination != "" {
		t.Fatalf("selection mutated enrollment inventory: %#v", bindings)
	}
}

func TestPlanRealFCMSelectionReusesCohortForIdempotentRetry(t *testing.T) {
	cohort := routeCohort("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	bindings := []realFCMBinding{
		{id: 20, installationID: "install-a", destination: "FCM_ACTIVE", cohort: cohort, revision: 2},
		{id: 21, installationID: "install-b", destination: "HELD", cohort: cohort, revision: 1},
	}

	got, mutations, err := planRealFCMSelection(bindings, "install-a")
	if err != nil {
		t.Fatalf("planRealFCMSelection() error = %v", err)
	}
	if got != uuid.UUID(cohort.Bytes) || len(mutations) != 0 {
		t.Fatalf("retry plan = (%s, %#v), want existing cohort and no route writes", got, mutations)
	}
}

func TestPlanRealFCMSelectionSwitchesFromMultipleActiveRoutes(t *testing.T) {
	firstCohort := routeCohort("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	secondCohort := routeCohort("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	bindings := []realFCMBinding{
		{id: 30, installationID: "install-a", destination: "FCM_ACTIVE", cohort: firstCohort, revision: 3},
		{id: 31, installationID: "install-b", destination: "FCM_ACTIVE", cohort: secondCohort, revision: 1},
	}

	cohort, mutations, err := planRealFCMSelection(bindings, "install-a")
	if err != nil {
		t.Fatalf("planRealFCMSelection() error = %v", err)
	}
	if cohort == uuid.Nil || cohort == uuid.UUID(firstCohort.Bytes) || cohort == uuid.UUID(secondCohort.Bytes) {
		t.Fatalf("switch cohort = %s, want a fresh cohort", cohort)
	}
	if len(mutations) != 3 || mutations[0].binding.installationID != "install-b" || mutations[0].destination != "HELD" ||
		mutations[1].binding.installationID != "install-a" || mutations[1].destination != "HELD" ||
		mutations[2].binding.installationID != "install-a" || mutations[2].destination != "FCM_ACTIVE" ||
		mutations[2].binding.revision != 4 {
		t.Fatalf("route mutations = %#v, want inactive old routes then fresh selected generation", mutations)
	}
}

func TestPlanRealFCMSelectionRejectsRetiredAndUnknownInstallations(t *testing.T) {
	bindings := []realFCMBinding{{id: 40, installationID: "retired", destination: "RETIRED"}}
	for _, id := range []string{"retired", "unknown"} {
		if _, _, err := planRealFCMSelection(bindings, id); err == nil {
			t.Fatalf("planRealFCMSelection(%q) accepted an unavailable installation", id)
		}
	}
}
