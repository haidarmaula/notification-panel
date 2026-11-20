package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database"
	"hello/internal/deployment"
	"hello/internal/jsonobject"
	"hello/internal/runloop"
)

var errPilotRoutePolicy = errors.New("invalid protected pilot route policy")

type pilotRoutePolicy struct {
	databaseURL string
	query       string
	args        []any
	grant       pgtype.UUID
	issue       bool
}

func parsePilotRoutePolicy(mode string, data []byte) (pilotRoutePolicy, error) {
	var policy pilotRoutePolicy
	var project, environment, grant, cohort, installation, expiry string
	fields := map[string]any{
		"database_url": &policy.databaseURL, "application_id": &project,
		"environment": &environment, "grant_id": &grant,
	}
	switch mode {
	case "approve-pilot-route":
		policy.issue = true
		fields["cohort_id"], fields["installation_id"], fields["expires_at"] = &cohort, &installation, &expiry
	case "apply-pilot-route":
	default:
		return pilotRoutePolicy{}, errPilotRoutePolicy
	}
	var appID pgtype.UUID
	if jsonobject.Decode(data, fields) != nil || policy.databaseURL == "" ||
		(environment != "staging" && environment != "production") ||
		!scanReleaseReviewID(project, &appID) || !scanReleaseReviewID(grant, &policy.grant) {
		return pilotRoutePolicy{}, errPilotRoutePolicy
	}
	if !policy.issue {
		policy.query = "SELECT public.apply_onboarding_pilot_route($1,$2,$3)::text"
		policy.args = []any{appID, environment, policy.grant}
		return policy, nil
	}
	var cohortID, installationID pgtype.UUID
	expires, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil || !scanReleaseReviewID(cohort, &cohortID) || !scanReleaseReviewID(installation, &installationID) {
		return pilotRoutePolicy{}, errPilotRoutePolicy
	}
	policy.query = "SELECT public.approve_onboarding_pilot_route($1,$2,$3,$4,$5,$6)::text"
	policy.args = []any{appID, environment, cohortID, installationID, policy.grant, expires}
	return policy, nil
}

func runPilotRoute(mode string) int {
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()

	path := "/etc/hello/pilot-route-approval.json"
	if mode == "apply-pilot-route" {
		path = "/etc/hello/pilot-route-application.json"
	}
	data, err := deployment.ReadSecret(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, errPilotRoutePolicy)
		return 1
	}
	policy, err := parsePilotRoutePolicy(mode, data)
	if err != nil {
		fmt.Fprintln(os.Stderr, errPilotRoutePolicy)
		return 1
	}
	cfg, err := deployment.Database(policy.databaseURL, "hello-pilot-route")
	if err != nil || deployment.CheckDatabaseFiles(policy.databaseURL) != nil {
		fmt.Fprintln(os.Stderr, errPilotRoutePolicy)
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
		fmt.Fprintln(os.Stderr, "pilot route database unavailable")
		return 1
	}
	defer pool.Close()
	var result string
	if err := pool.QueryRow(ctx, policy.query, policy.args...).Scan(&result); err != nil || !validPilotRouteResult(policy, result) {
		fmt.Fprintln(os.Stderr, "pilot route unavailable; retry only with the same protected policy")
		return 1
	}
	if _, err := fmt.Fprintln(os.Stdout, result); err != nil {
		return 1
	}
	return 0
}

func validPilotRouteResult(policy pilotRoutePolicy, result string) bool {
	if policy.issue {
		return result == policy.grant.String()
	}
	revision, err := strconv.ParseInt(result, 10, 64)
	return err == nil && revision > 0
}
