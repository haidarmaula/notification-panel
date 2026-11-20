package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"hello/internal/database/sqlc"
)

func TestRejectsPolicyPathOverride(t *testing.T) {
	for _, args := range [][]string{
		{"--policy", "/untrusted/config.json"},
		{"coordinate-cohort", "/untrusted/config.json"},
		{"verify-cohort", "--policy", "/untrusted/config.json"},
	} {
		if code := run(args); code != 2 {
			t.Fatalf("unexpected exit code: %d", code)
		}
	}
}

const coordinatorJSON = `{"application_id":"11111111-1111-4111-8111-111111111111","operation_id":"22222222-2222-4222-8222-222222222222","environment":"staging","database_url":"protected"}`

func TestCoordinatorPolicy(t *testing.T) {
	_, scope, err := parseCoordinator([]byte(coordinatorJSON))
	if err != nil || scope.Environment != "staging" || !scope.Project.Valid || !scope.Operation.Valid || scope.Owner.Valid {
		t.Fatal("valid scope was not parsed")
	}
	for _, body := range []string{
		strings.Replace(coordinatorJSON, "staging", "any", 1),
		strings.Replace(coordinatorJSON, "22222222-2222-4222-8222-222222222222", "00000000-0000-0000-0000-000000000000", 1),
		strings.TrimSuffix(coordinatorJSON, "}") + `,"owner":"override"}`,
		strings.TrimSuffix(coordinatorJSON, "}") + `,"environment":"production"}`,
	} {
		if _, _, err := parseCoordinator([]byte(body)); !errors.Is(err, errCohortPolicy) {
			t.Fatal("accepted invalid coordinator policy")
		}
	}
}

func TestCohortVerifierPolicy(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	fingerprint := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("f", 32)))
	body := fmt.Sprintf(`{"verifier":{"application_id":"11111111-1111-4111-8111-111111111111","environment":"staging","credential_root":"/etc/hello/credentials","database_url":"protected"},"current_encryption_version":2,"fingerprint_key":%q,"encryption_keys":[{"version":2,"key":%q}]}`, fingerprint, key)
	policy, protector, err := parseCohortVerifier([]byte(body))
	if err != nil || protector == nil || policy.Environment != "staging" {
		t.Fatal("valid verifier policy was rejected")
	}
	for _, invalid := range []string{
		strings.Replace(body, "staging", "any", 1),
		strings.Replace(body, key, "invalid-base64", 1),
		strings.Replace(body, fingerprint, key, 1),
		strings.Replace(body, `"current_encryption_version":2`, `"current_encryption_version":1`, 1),
		strings.Replace(body, `"version":2`, `"version":2,"version":3`, 1),
	} {
		if _, protector, err := parseCohortVerifier([]byte(invalid)); protector != nil || !errors.Is(err, errCohortPolicy) {
			t.Fatal("accepted invalid verifier policy")
		}
	}
}

type coordinatorQueriesStub struct {
	initialize func(sqlc.InitializeOnboardingCohortParams) (int64, error)
	finalize   func(sqlc.FinalizeOnboardingCohortParams) (bool, error)
}

func (s coordinatorQueriesStub) InitializeOnboardingCohort(_ context.Context, p sqlc.InitializeOnboardingCohortParams) (int64, error) {
	return s.initialize(p)
}

func (s coordinatorQueriesStub) FinalizeOnboardingCohort(_ context.Context, p sqlc.FinalizeOnboardingCohortParams) (bool, error) {
	return s.finalize(p)
}

func TestCoordinatorReacquiresFenceBeforeFinalizing(t *testing.T) {
	_, scope, err := parseCoordinator([]byte(coordinatorJSON))
	if err != nil {
		t.Fatal(err)
	}
	scope.Owner = scope.Project
	attempt := 0
	step := coordinatorStep(coordinatorQueriesStub{
		initialize: func(p sqlc.InitializeOnboardingCohortParams) (int64, error) {
			if p != scope {
				t.Fatalf("wrong initialization scope: %v", p)
			}
			attempt++
			if attempt == 1 {
				return 0, pgx.ErrNoRows
			}
			return int64(attempt), nil
		},
		finalize: func(p sqlc.FinalizeOnboardingCohortParams) (bool, error) {
			want := sqlc.FinalizeOnboardingCohortParams{Project: scope.Project, Operation: scope.Operation, Environment: scope.Environment, Owner: scope.Owner, LeaseToken: int64(attempt)}
			if attempt < 2 || p != want {
				t.Fatalf("wrong finalization fence: %v", p)
			}
			return attempt == 3, nil
		},
	}, scope)
	for _, want := range []bool{false, false, true} {
		if applied, err := step(t.Context()); err != nil || applied != want {
			t.Fatalf("step: applied %v, error %v; want applied %v", applied, err, want)
		}
	}
}
