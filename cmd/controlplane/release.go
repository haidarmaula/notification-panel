package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hello/internal/database"
	"hello/internal/deployment"
	"hello/internal/runloop"
)

const releasePolicyPath = "/etc/hello/dispatch-release.json"

func runRelease() int {
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()
	data, err := deployment.ReadSecret(releasePolicyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid dispatch release policy")
		return 1
	}
	// Both one-shot commands accept the same exact operation/grant/instance tuple.
	policy, err := parseFirstActivationPolicy(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid dispatch release policy")
		return 1
	}
	dbConfig, err := deployment.Database(policy.databaseURL, "hello-dispatch-release")
	if err != nil || deployment.CheckDatabaseFiles(policy.databaseURL) != nil {
		fmt.Fprintln(os.Stderr, "invalid dispatch release database configuration")
		return 1
	}
	dbConfig.MaxConns, dbConfig.MinConns = 1, 0
	dbConfig.ConnConfig.RuntimeParams["search_path"] = "pg_catalog,pg_temp"
	dbConfig.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer runloop.InterruptOutputOnCancel(ctx, os.Stdout, os.Stderr)()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, err := database.NewWithConfig(ctx, dbConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dispatch release database unavailable")
		return 1
	}
	defer pool.Close()
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT public.apply_onboarding_release($1,$2,$3)`,
		policy.operation, policy.grant, policy.instance).Scan(&revision); err != nil || revision <= 0 {
		fmt.Fprintln(os.Stderr, "dispatch release result unavailable; inspect the operation before retrying")
		return 1
	}
	if _, err := fmt.Fprintln(os.Stdout, revision); err != nil {
		return 1
	}
	return 0
}
