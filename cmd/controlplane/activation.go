package main

import (
	"context"
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

const firstActivationPolicyPath = "/etc/hello/first-activation.json"

var errActivationPolicy = errors.New("invalid first activation policy")

type firstActivationPolicy struct {
	databaseURL string
	operation   pgtype.UUID
	grant       pgtype.UUID
	instance    pgtype.UUID
}

func parseFirstActivationPolicy(data []byte) (firstActivationPolicy, error) {
	var policy firstActivationPolicy
	var operation, grant, instance string
	if err := jsonobject.Decode(data, map[string]any{
		"database_url": &policy.databaseURL, "operation_id": &operation,
		"grant_id": &grant, "instance_id": &instance,
	}); err != nil || policy.databaseURL == "" {
		return firstActivationPolicy{}, errActivationPolicy
	}
	for _, field := range []struct {
		raw string
		id  *pgtype.UUID
	}{{operation, &policy.operation}, {grant, &policy.grant}, {instance, &policy.instance}} {
		if field.id.Scan(field.raw) != nil || !field.id.Valid || field.id.Bytes == [16]byte{} || field.id.String() != field.raw {
			return firstActivationPolicy{}, errActivationPolicy
		}
	}
	return policy, nil
}

func runFirstActivation() int {
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()
	data, err := deployment.ReadSecret(firstActivationPolicyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errActivationPolicy)
		return 1
	}
	policy, err := parseFirstActivationPolicy(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, errActivationPolicy)
		return 1
	}
	dbConfig, err := deployment.Database(policy.databaseURL, "hello-first-activation")
	if err != nil || deployment.CheckDatabaseFiles(policy.databaseURL) != nil {
		fmt.Fprintln(os.Stderr, errActivationPolicy)
		return 1
	}
	dbConfig.MaxConns, dbConfig.MinConns = 1, 0
	dbConfig.ConnConfig.RuntimeParams["search_path"] = "pg_catalog,pg_temp"
	dbConfig.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	dbConfig.ConnConfig.RuntimeParams["lock_timeout"] = "2000"
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer runloop.InterruptOutputOnCancel(ctx, os.Stdout, os.Stderr)()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, err := database.NewWithConfig(ctx, dbConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "activation database unavailable")
		return 1
	}
	defer pool.Close()
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT public.apply_onboarding_first_activation($1,$2,$3)`,
		policy.operation, policy.grant, policy.instance).Scan(&revision); err != nil || revision <= 0 {
		fmt.Fprintln(os.Stderr, "activation result unavailable; inspect the operation before retrying")
		return 1
	}
	if _, err := fmt.Fprintln(os.Stdout, revision); err != nil {
		return 1
	}
	return 0
}
