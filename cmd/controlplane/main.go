// Command controlplane processes scoped onboarding OAuth verification work.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hello/internal/database"
	"hello/internal/database/sqlc"
	"hello/internal/deployment"
	"hello/internal/onboarding"
	"hello/internal/runloop"
)

const controlplanePolicyPath = "/etc/hello/controlplane.json"

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	signal.Ignore(syscall.SIGPIPE)
	if len(args) == 1 {
		switch args[0] {
		case "prepare-runtime", "preflight-delivery", "start-delivery":
			return runRuntimePreparation(args[0])
		case "apply-first-activation":
			return runFirstActivation()
		}
	}
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "controlplane accepts prepare-runtime, preflight-delivery, start-delivery or apply-first-activation")
		return 2
	}
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()
	data, err := deployment.ReadSecret(controlplanePolicyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid control-plane configuration")
		return 1
	}
	policy, err := onboarding.ParseVerifierPolicy(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid control-plane configuration")
		return 1
	}
	databaseConfig, err := deployment.Database(policy.DatabaseURL, "hello-controlplane")
	if err != nil || deployment.CheckDatabaseFiles(policy.DatabaseURL) != nil {
		fmt.Fprintln(os.Stderr, "invalid control-plane database configuration")
		return 1
	}
	databaseConfig.MaxConns = 2
	databaseConfig.MinConns = 0
	databaseConfig.ConnConfig.RuntimeParams["search_path"] = "pg_catalog,pg_temp"
	databaseConfig.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	databaseConfig.ConnConfig.RuntimeParams["lock_timeout"] = "2000"
	databaseConfig.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5000"

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer runloop.InterruptOutputOnCancel(ctx, os.Stdout, os.Stderr)()
	startupCtx, cancelStartup := context.WithTimeout(ctx, 10*time.Second)
	pool, err := database.NewWithConfig(startupCtx, databaseConfig)
	cancelStartup()
	if err != nil {
		fmt.Fprintln(os.Stderr, "control-plane database unavailable")
		return 1
	}
	defer pool.Close()
	verifier, err := onboarding.NewVerifier(sqlc.New(pool), policy.ApplicationID, policy.Environment, policy.CredentialRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid control-plane verifier configuration")
		return 1
	}

	for ctx.Err() == nil {
		worked, runErr := verifier.RunOnce(ctx)
		if runErr != nil {
			fmt.Fprintln(os.Stderr, "onboarding verification attempt unavailable")
		}
		if worked && runErr == nil {
			continue
		}
		timer := time.NewTimer(policy.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return 0
}
