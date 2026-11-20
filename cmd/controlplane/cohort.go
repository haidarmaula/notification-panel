package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"hello/internal/database/sqlc"
	"hello/internal/deployment"
	"hello/internal/installation"
	"hello/internal/jsonobject"
	"hello/internal/onboarding"
	"hello/internal/runloop"
)

const (
	coordinatorPolicyPath    = "/etc/hello/cohort-coordinator.json"
	cohortVerifierPolicyPath = "/etc/hello/cohort-verifier.json"
)

var errCohortPolicy = errors.New("invalid cohort worker policy")

func runCohort(mode string) int {
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()

	path := coordinatorPolicyPath
	if mode == "verify-cohort" {
		path = cohortVerifierPolicyPath
	}
	data, err := deployment.ReadSecret(path)
	if err == nil {
		if mode == "verify-cohort" {
			policy, protector, parseErr := parseCohortVerifier(data)
			if parseErr == nil {
				return runProtectedWorker(policy.DatabaseURL, "hello-cohort-verifier", policy.PollInterval, func(pool *pgxpool.Pool) (workStep, error) {
					store, buildErr := onboarding.NewPostgresCohortStore(pool, protector)
					if buildErr != nil {
						return nil, buildErr
					}
					verifier, buildErr := onboarding.NewCohortVerifier(store, policy)
					if buildErr != nil {
						return nil, buildErr
					}
					return verifier.RunOnce, nil
				})
			}
		} else {
			dsn, scope, parseErr := parseCoordinator(data)
			if parseErr == nil {
				return runProtectedWorker(dsn, "hello-cohort-coordinator", time.Second, func(pool *pgxpool.Pool) (workStep, error) {
					owner, buildErr := uuid.NewRandom()
					if buildErr != nil {
						return nil, buildErr
					}
					scope.Owner = pgtype.UUID{Bytes: owner, Valid: true}
					queries := sqlc.New(pool)
					if scope.Operation.Valid {
						return coordinatorStep(queries, scope), nil
					}
					return discoveringCoordinatorStep(pool, queries, scope), nil
				})
			}
		}
	}
	fmt.Fprintln(os.Stderr, "invalid cohort worker configuration")
	return 1
}

func parseCoordinator(data []byte) (string, sqlc.InitializeOnboardingCohortParams, error) {
	var scope sqlc.InitializeOnboardingCohortParams
	var dsn, app string
	var operation *string
	if !validCohortJSON(data) || jsonobject.DecodeOptional(data, map[string]any{
		"database_url": &dsn, "application_id": &app, "environment": &scope.Environment,
	}, map[string]any{"operation_id": &operation}) != nil || !scanCohortUUID(app, &scope.Project) ||
		(operation != nil && !scanCohortUUID(*operation, &scope.Operation)) || dsn == "" ||
		(scope.Environment != string(onboarding.EnvironmentStaging) && scope.Environment != string(onboarding.EnvironmentProduction)) {
		return "", sqlc.InitializeOnboardingCohortParams{}, errCohortPolicy
	}
	return dsn, scope, nil
}

func scanCohortUUID(s string, id *pgtype.UUID) bool {
	return len(s) == 36 && id.Scan(s) == nil && id.Valid && id.Bytes != [16]byte{} && id.String() == s
}

func validCohortJSON(data []byte) bool {
	return len(data) <= 64*1024 && utf8.Valid(data) && jsonobject.UniqueLowercaseFields(data)
}

func parseCohortVerifier(data []byte) (onboarding.VerifierPolicy, *installation.Protector, error) {
	var policy onboarding.VerifierPolicy
	var rawPolicy json.RawMessage
	var rawKeys []json.RawMessage
	var current int32
	var fingerprint string
	if !validCohortJSON(data) || jsonobject.Decode(data, map[string]any{
		"verifier": &rawPolicy, "current_encryption_version": &current,
		"fingerprint_key": &fingerprint, "encryption_keys": &rawKeys,
	}) != nil || len(rawKeys) == 0 || len(rawKeys) > 64 {
		return policy, nil, errCohortPolicy
	}
	policy, err := onboarding.ParseVerifierPolicy(rawPolicy)
	if err != nil {
		return onboarding.VerifierPolicy{}, nil, errCohortPolicy
	}
	keys := make(map[int32][]byte, len(rawKeys))
	for _, raw := range rawKeys {
		var version int32
		var value string
		if jsonobject.Decode(raw, map[string]any{"version": &version, "key": &value}) != nil || keys[version] != nil {
			return onboarding.VerifierPolicy{}, nil, errCohortPolicy
		}
		key := decodeCohortKey(value)
		if key == nil {
			return onboarding.VerifierPolicy{}, nil, errCohortPolicy
		}
		keys[version] = key
	}
	protector, err := installation.NewProtector(current, keys, decodeCohortKey(fingerprint))
	if err != nil {
		return onboarding.VerifierPolicy{}, nil, errCohortPolicy
	}
	return policy, protector, nil
}

func decodeCohortKey(s string) []byte {
	key, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || len(key) != 32 || base64.StdEncoding.EncodeToString(key) != s {
		return nil
	}
	return key
}

type cohortCoordinatorQueries interface {
	InitializeOnboardingCohort(context.Context, sqlc.InitializeOnboardingCohortParams) (int64, error)
	FinalizeOnboardingCohort(context.Context, sqlc.FinalizeOnboardingCohortParams) (bool, error)
}

func coordinatorStep(queries cohortCoordinatorQueries, scope sqlc.InitializeOnboardingCohortParams) workStep {
	return func(ctx context.Context) (bool, error) {
		attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		// Reacquire the fence on every poll. SQL preserves the live token for
		// this owner and issues a new token only on eligible takeover.
		token, err := queries.InitializeOnboardingCohort(attempt, scope)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return queries.FinalizeOnboardingCohort(attempt, sqlc.FinalizeOnboardingCohortParams{
			Project: scope.Project, Environment: scope.Environment, Operation: scope.Operation,
			Owner: scope.Owner, LeaseToken: token,
		})
	}
}

// Discover before taking the lease so a completed or concurrently held
// operation never makes this long-lived coordinator apply a stale request.
func discoveringCoordinatorStep(pool *pgxpool.Pool, queries cohortCoordinatorQueries, scope sqlc.InitializeOnboardingCohortParams) workStep {
	return func(ctx context.Context) (bool, error) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		attempt := scope
		err := pool.QueryRow(ctx, `SELECT public.find_onboarding_cohort_operation($1,$2)`, scope.Project, scope.Environment).Scan(&attempt.Operation)
		if err != nil || !attempt.Operation.Valid {
			return false, err
		}
		return coordinatorStep(queries, attempt)(ctx)
	}
}
