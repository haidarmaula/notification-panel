package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database"
	"hello/internal/deployment"
	"hello/internal/jsonobject"
	"hello/internal/runloop"
)

const firstActivationApprovalPath = "/etc/hello/first-activation-approval.json"

var activationApprovalRole = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

type firstActivationApproval struct {
	databaseURL string
	project     pgtype.UUID
	operation   pgtype.UUID
	grant       pgtype.UUID
	executor    string
	generation  int64
	ownership   []byte
	writerFence []byte
	cohortFence []byte
	retired     []string
}

func parseFirstActivationApproval(data []byte) (firstActivationApproval, error) {
	var approval firstActivationApproval
	var project, operation, grant, ownership, writerFence, cohortFence string
	if err := jsonobject.Decode(data, map[string]any{
		"database_url": &approval.databaseURL, "application_id": &project, "operation_id": &operation,
		"grant_id": &grant, "executor": &approval.executor, "generation": &approval.generation,
		"ownership_sha256": &ownership, "writer_fence_sha256": &writerFence, "cohort_fence_sha256": &cohortFence,
		"retired_writer_roles": &approval.retired,
	}); err != nil || approval.databaseURL == "" || approval.generation < 1 ||
		!activationApprovalRole.MatchString(approval.executor) || len(approval.retired) > 64 {
		return firstActivationApproval{}, errActivationPolicy
	}
	for _, field := range []struct {
		raw string
		id  *pgtype.UUID
	}{{project, &approval.project}, {operation, &approval.operation}, {grant, &approval.grant}} {
		if !scanCohortUUID(field.raw, field.id) {
			return firstActivationApproval{}, errActivationPolicy
		}
	}
	for _, role := range approval.retired {
		if !activationApprovalRole.MatchString(role) {
			return firstActivationApproval{}, errActivationPolicy
		}
	}
	for _, field := range []struct {
		raw    string
		digest *[]byte
	}{{ownership, &approval.ownership}, {writerFence, &approval.writerFence}, {cohortFence, &approval.cohortFence}} {
		digest, err := hex.DecodeString(field.raw)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != field.raw {
			return firstActivationApproval{}, errActivationPolicy
		}
		*field.digest = digest
	}
	return approval, nil
}

func runIssueFirstActivation() int {
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()
	data, err := deployment.ReadSecret(firstActivationApprovalPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, errActivationPolicy)
		return 1
	}
	approval, err := parseFirstActivationApproval(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, errActivationPolicy)
		return 1
	}
	config, err := deployment.Database(approval.databaseURL, "hello-issue-first-activation")
	if err != nil || deployment.CheckDatabaseFiles(approval.databaseURL) != nil {
		fmt.Fprintln(os.Stderr, "invalid activation issuer database configuration")
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
		fmt.Fprintln(os.Stderr, "activation issuer database unavailable")
		return 1
	}
	defer pool.Close()
	var issued pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT public.issue_onboarding_first_activation($1,$2,$3,$4,$5,$6,$7,$8,$9::text[]::name[])`,
		approval.project, approval.operation, approval.grant, approval.executor, approval.generation,
		approval.ownership, approval.writerFence, approval.cohortFence, approval.retired,
	).Scan(&issued); err != nil || issued != approval.grant {
		fmt.Fprintln(os.Stderr, "activation approval unavailable; inspect the operation before retrying")
		return 1
	}
	if _, err := fmt.Fprintln(os.Stdout, issued.String()); err != nil {
		return 1
	}
	return 0
}
