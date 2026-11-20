package main

import (
	"context"
	"encoding/hex"
	"errors"
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

var errReleaseReviewPolicy = errors.New("invalid protected release review policy")

type releaseReviewPolicy struct {
	databaseURL string
	query       string
	args        []any
	id          pgtype.UUID
}

func parseReleaseReviewPolicy(mode string, data []byte) (releaseReviewPolicy, error) {
	var policy releaseReviewPolicy
	var project, environment, expiry, id, kind, configuration, cohort, evidence, operation string
	var legacy, consent, coverage, quota string
	var generation int64
	fields := map[string]any{
		"database_url": &policy.databaseURL, "application_id": &project,
		"environment": &environment, "expires_at": &expiry,
	}
	switch mode {
	case "approve-release":
		fields["approval_id"], fields["mode"] = &id, &kind
		fields["configuration_id"], fields["cohort_id"], fields["evidence_sha256"] = &configuration, &cohort, &evidence
	case "delegate-release":
		fields["grant_id"], fields["operation_id"], fields["runtime_generation"] = &id, &operation, &generation
		fields["legacy_sha256"], fields["consent_sha256"] = &legacy, &consent
		fields["coverage_sha256"], fields["quota_sha256"] = &coverage, &quota
	default:
		return releaseReviewPolicy{}, errReleaseReviewPolicy
	}
	if jsonobject.Decode(data, fields) != nil || policy.databaseURL == "" ||
		(environment != "staging" && environment != "production") {
		return releaseReviewPolicy{}, errReleaseReviewPolicy
	}
	var app pgtype.UUID
	expires, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil || !scanReleaseReviewID(project, &app) || !scanReleaseReviewID(id, &policy.id) {
		return releaseReviewPolicy{}, errReleaseReviewPolicy
	}
	if mode == "approve-release" {
		var configID, cohortID pgtype.UUID
		digest, ok := releaseReviewDigest(evidence)
		if !ok || (kind != "PILOT" && kind != "FULL") ||
			!scanReleaseReviewID(configuration, &configID) || !scanReleaseReviewID(cohort, &cohortID) {
			return releaseReviewPolicy{}, errReleaseReviewPolicy
		}
		policy.query = "SELECT public.approve_onboarding_release($1,$2,$3,$4,$5,$6,$7,$8)"
		policy.args = []any{app, environment, policy.id, kind, configID, cohortID, digest, expires}
		return policy, nil
	}
	var opID pgtype.UUID
	if generation <= 0 || !scanReleaseReviewID(operation, &opID) {
		return releaseReviewPolicy{}, errReleaseReviewPolicy
	}
	policy.query = "SELECT public.delegate_onboarding_release($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)"
	policy.args = []any{app, environment, opID, policy.id, generation}
	for _, raw := range []string{legacy, consent, coverage, quota} {
		digest, ok := releaseReviewDigest(raw)
		if !ok {
			return releaseReviewPolicy{}, errReleaseReviewPolicy
		}
		policy.args = append(policy.args, digest)
	}
	policy.args = append(policy.args, expires)
	return policy, nil
}

func scanReleaseReviewID(raw string, id *pgtype.UUID) bool {
	return id.Scan(raw) == nil && id.Valid && id.Bytes != [16]byte{} && id.String() == raw
}

func releaseReviewDigest(raw string) ([]byte, bool) {
	value, err := hex.DecodeString(raw)
	return value, err == nil && len(value) == 32 && hex.EncodeToString(value) == raw
}

func runReleaseReview(mode string) int {
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()

	path := "/etc/hello/dispatch-release-approval.json"
	if mode == "delegate-release" {
		path = "/etc/hello/dispatch-release-delegation.json"
	}
	data, err := deployment.ReadSecret(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, errReleaseReviewPolicy)
		return 1
	}
	policy, err := parseReleaseReviewPolicy(mode, data)
	if err != nil {
		fmt.Fprintln(os.Stderr, errReleaseReviewPolicy)
		return 1
	}
	cfg, err := deployment.Database(policy.databaseURL, "hello-release-review")
	if err != nil || deployment.CheckDatabaseFiles(policy.databaseURL) != nil {
		fmt.Fprintln(os.Stderr, errReleaseReviewPolicy)
		return 1
	}
	cfg.MaxConns, cfg.MinConns = 1, 0
	cfg.ConnConfig.RuntimeParams["search_path"] = "pg_catalog,pg_temp"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer runloop.InterruptOutputOnCancel(ctx, os.Stdout, os.Stderr)()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, err := database.NewWithConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "release review database unavailable")
		return 1
	}
	defer pool.Close()
	var result pgtype.UUID
	if err := pool.QueryRow(ctx, policy.query, policy.args...).Scan(&result); err != nil || result != policy.id {
		fmt.Fprintln(os.Stderr, "release review unavailable; retry only with the same protected policy")
		return 1
	}
	if _, err := fmt.Fprintln(os.Stdout, result.String()); err != nil {
		return 1
	}
	return 0
}
