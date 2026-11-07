// Command retention previews or applies one database-authorized expiry batch.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"hello/internal/database"
	"hello/internal/database/sqlc"
	"hello/internal/deployment"
	"hello/internal/jsonobject"
	"hello/internal/runloop"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	signal.Ignore(syscall.SIGPIPE)
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer runloop.InterruptOutputOnCancel(ctx, os.Stdout, os.Stderr)()

	const usage = "Usage: retention {preview|apply} BATCH_NUMBER"
	if len(args) == 1 && args[0] == "--help" {
		fmt.Fprintln(os.Stdout, usage)
		return 0
	}
	step, apply, ok := parseStep(args)
	if !ok {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	data, err := deployment.Read(deployment.Retention)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid retention configuration")
		return 1
	}
	cfg, batch, err := policy(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid retention configuration")
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := database.NewWithConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "retention database unavailable")
		return 1
	}
	defer pool.Close()
	batch.BatchNo, batch.Apply = step, apply
	outcome, err := sqlc.New(pool).RunRetentionBatch(ctx, batch)
	if err != nil {
		fmt.Fprintln(os.Stderr, "retention batch unavailable; inspect protected job evidence before retry")
		return 1
	}
	if _, err := fmt.Fprintf(os.Stdout, "batch=%d outcome=%s count=%d\n", step, outcome.Outcome, outcome.Quantity); err != nil {
		return 1
	}
	return 0
}

func parseStep(args []string) (int32, bool, bool) {
	if len(args) != 2 || (args[0] != "preview" && args[0] != "apply") {
		return 0, false, false
	}
	n, err := strconv.ParseInt(args[1], 10, 32)
	if err != nil || n < 1 || n > 1000 || strconv.FormatInt(n, 10) != args[1] {
		return 0, false, false
	}
	return int32(n), args[0] == "apply", true
}

func policy(data []byte) (*pgxpool.Config, sqlc.RunRetentionBatchParams, error) {
	var batch sqlc.RunRetentionBatchParams
	var dsn, grant, project, origin, source string
	err := jsonobject.Decode(data, map[string]any{
		"database_url": &dsn, "grant_id": &grant, "application_id": &project,
		"origin_epoch": &origin, "source_epoch": &source, "record_class": &batch.RecordClass,
	})
	if err != nil || (batch.RecordClass != "origin_nonce" && batch.RecordClass != "receipt_request" && batch.RecordClass != "send_key") ||
		!parseID(grant, &batch.GrantID) || !parseID(project, &batch.ApplicationPublicID) ||
		!parseID(origin, &batch.OriginEpoch) || !parseID(source, &batch.SourceEpoch) {
		return nil, sqlc.RunRetentionBatchParams{}, deployment.ErrConfiguration
	}
	cfg, err := deployment.Database(dsn, "hello-retention")
	if err != nil || deployment.CheckDatabaseFiles(dsn) != nil {
		return nil, sqlc.RunRetentionBatchParams{}, deployment.ErrConfiguration
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "3000"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "250"
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "3000"
	cfg.ConnConfig.RuntimeParams["search_path"] = "pg_catalog,pg_temp"
	return cfg, batch, nil
}

func parseID(value string, id *pgtype.UUID) bool {
	return id.Scan(value) == nil && id.Valid && id.Bytes != [16]byte{} && id.String() == value
}
