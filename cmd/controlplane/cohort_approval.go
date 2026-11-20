package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"hello/internal/database"
	"hello/internal/deployment"
	"hello/internal/jsonobject"
	"hello/internal/runloop"
)

const cohortApprovalPolicyPath = "/etc/hello/cohort-approval.json"

type cohortApprovalPolicy struct {
	databaseURL, environment, issuer     string
	application, approval, configuration pgtype.UUID
	installations                        []pgtype.UUID
	evidence                             []byte
	expires                              time.Time
}

func parseCohortApproval(data []byte) (cohortApprovalPolicy, error) {
	var result cohortApprovalPolicy
	var app, approval, configuration, digest, expires string
	var installations []string
	if !validCohortJSON(data) || jsonobject.Decode(data, map[string]any{
		"database_url": &result.databaseURL, "application_id": &app, "environment": &result.environment,
		"approval_id": &approval, "configuration_id": &configuration, "installation_ids": &installations,
		"issuer_key_id": &result.issuer, "evidence_sha256": &digest, "expires_at": &expires,
	}) != nil || result.databaseURL == "" || (result.environment != "staging" && result.environment != "production") ||
		len(result.issuer) < 1 || len(result.issuer) > 128 || len(installations) < 1 || len(installations) > 10 ||
		!scanCohortUUID(app, &result.application) || !scanCohortUUID(approval, &result.approval) ||
		!scanCohortUUID(configuration, &result.configuration) {
		return cohortApprovalPolicy{}, errCohortPolicy
	}
	var err error
	result.evidence, err = hex.DecodeString(digest)
	if err != nil || len(result.evidence) != 32 || digest != hex.EncodeToString(result.evidence) {
		return cohortApprovalPolicy{}, errCohortPolicy
	}
	result.expires, err = time.Parse(time.RFC3339, expires)
	if err != nil {
		return cohortApprovalPolicy{}, errCohortPolicy
	}
	seen := make(map[pgtype.UUID]bool, len(installations))
	for _, raw := range installations {
		var id pgtype.UUID
		if !scanCohortUUID(raw, &id) || seen[id] {
			return cohortApprovalPolicy{}, errCohortPolicy
		}
		seen[id] = true
		result.installations = append(result.installations, id)
	}
	return result, nil
}

// runCohortApproval imports an externally reviewed roster through an app-scoped
// importer LOGIN. The policy's evidence digest identifies the consent review.
func runCohortApproval() int {
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()
	data, err := deployment.ReadSecret(cohortApprovalPolicyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid cohort approval policy")
		return 1
	}
	policy, err := parseCohortApproval(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid cohort approval policy")
		return 1
	}
	config, err := deployment.Database(policy.databaseURL, "hello-cohort-approval")
	if err != nil || deployment.CheckDatabaseFiles(policy.databaseURL) != nil {
		fmt.Fprintln(os.Stderr, "invalid cohort approval database configuration")
		return 1
	}
	config.MaxConns, config.MinConns = 1, 0
	config.ConnConfig.RuntimeParams["search_path"] = "pg_catalog,pg_temp"
	config.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer runloop.InterruptOutputOnCancel(ctx, os.Stdout, os.Stderr)()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, err := database.NewWithConfig(ctx, config)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cohort approval database unavailable")
		return 1
	}
	defer pool.Close()
	var approved pgtype.UUID
	err = pool.QueryRow(ctx, `SELECT public.approve_onboarding_cohort($1,$2,$3,$4,$5,$6,$7,$8)`,
		policy.application, policy.environment, policy.approval, policy.configuration,
		policy.installations, policy.issuer, policy.evidence, policy.expires).Scan(&approved)
	if err != nil || approved != policy.approval {
		fmt.Fprintln(os.Stderr, "cohort approval result unavailable; inspect approvals before retrying")
		return 1
	}
	if _, err := fmt.Fprintln(os.Stdout, approved.String()); err != nil {
		return 1
	}
	return 0
}
