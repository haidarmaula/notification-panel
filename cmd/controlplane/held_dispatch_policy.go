package main

import (
	"context"
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

const heldDispatchPolicyPath = "/etc/hello/held-dispatch-policy.json"

type heldDispatchPolicy struct {
	databaseURL     string
	applicationID   pgtype.UUID
	environment     string
	profileRevision int64
	pushTTL         int32
	keyTTL          int32
}

func parseHeldDispatchPolicy(data []byte) (heldDispatchPolicy, error) {
	var policy heldDispatchPolicy
	var project string
	if err := jsonobject.Decode(data, map[string]any{
		"database_url": &policy.databaseURL, "application_id": &project, "environment": &policy.environment,
		"profile_revision": &policy.profileRevision, "push_ttl_seconds": &policy.pushTTL, "key_ttl_seconds": &policy.keyTTL,
	}); err != nil || !scanCohortUUID(project, &policy.applicationID) || policy.databaseURL == "" ||
		(policy.environment != "staging" && policy.environment != "production") || policy.profileRevision < 1 ||
		policy.pushTTL < 1 || policy.pushTTL > 604800 || policy.keyTTL < 1 || policy.keyTTL > 604800 {
		return heldDispatchPolicy{}, errActivationPolicy
	}
	return policy, nil
}

func runBootstrapHeldDispatch() int {
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()
	data, err := deployment.ReadSecret(heldDispatchPolicyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid held dispatch policy")
		return 1
	}
	policy, err := parseHeldDispatchPolicy(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid held dispatch policy")
		return 1
	}
	config, err := deployment.Database(policy.databaseURL, "hello-bootstrap-held-dispatch")
	if err != nil || deployment.CheckDatabaseFiles(policy.databaseURL) != nil {
		fmt.Fprintln(os.Stderr, "invalid dispatch bootstrap database configuration")
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
		fmt.Fprintln(os.Stderr, "dispatch bootstrap database unavailable")
		return 1
	}
	defer pool.Close()
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT public.bootstrap_onboarding_held_dispatch($1,$2,$3,$4,$5)`,
		policy.applicationID, policy.environment, policy.profileRevision, policy.pushTTL, policy.keyTTL,
	).Scan(&revision); err != nil || revision != 1 {
		fmt.Fprintln(os.Stderr, "held dispatch policy bootstrap unavailable")
		return 1
	}
	if _, err := fmt.Fprintln(os.Stdout, revision); err != nil {
		return 1
	}
	return 0
}
