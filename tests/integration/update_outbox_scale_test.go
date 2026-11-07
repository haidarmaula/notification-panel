//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
)

// Explain the actual parameterized statement used by sqlc on this transaction.
// Duplicating the query to reproduce its plan can hide regressions in the real selector.
type queryPlanProbe struct {
	sqlc.DBTX
	statement string
	args      []any
}

func (p *queryPlanProbe) QueryRow(ctx context.Context, statement string, args ...any) pgx.Row {
	p.statement, p.args = statement, args
	return p.DBTX.QueryRow(ctx, statement, args...)
}

func checkScopedOutboxSelection(t *testing.T, ctx context.Context, pool *pgxpool.Pool, event kafka.ScopedSendRequested) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	// Fill half the prepared deliveries. The publication codec has separate
	// coverage; only IDs, ordering, cardinality and envelope size affect this plan.
	tag, err := tx.Exec(ctx, `INSERT INTO notification_update_outbox(event_id, application_id, notification_id, delivery_id,
        delivery_version, origin_epoch, source_epoch, payload, occurred_at)
        SELECT gen_random_uuid(),d.application_id,d.notification_id,d.id,1,n.origin_epoch,n.source_epoch,
            convert_to(repeat('x',768),'UTF8'),statement_timestamp()
        FROM notification_delivery_intents d JOIN notifications n ON n.id=d.notification_id
        WHERE d.notification_id=$1 ORDER BY d.id LIMIT 10000`, event.NotificationID)
	require.NoError(t, err)
	require.EqualValues(t, 10000, tag.RowsAffected())
	_, err = tx.Exec(ctx, "ANALYZE notification_delivery_intents, notification_update_outbox")
	require.NoError(t, err)
	var args sqlc.LockNextDeliveryUpdateParams
	require.NoError(t, args.ApplicationPublicID.Scan(event.ApplicationID))
	require.NoError(t, args.OriginEpoch.Scan(event.OriginEpoch))
	require.NoError(t, args.SourceEpoch.Scan(event.SourceEpoch))
	probe := &queryPlanProbe{DBTX: tx}
	row, err := sqlc.New(probe).LockNextDeliveryUpdate(ctx, args)
	require.NoError(t, err)
	require.Equal(t, args.ApplicationPublicID, row.ApplicationPublicID)
	var raw []byte
	require.NoError(t, tx.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, TIMING OFF, FORMAT JSON) "+probe.statement, probe.args...).Scan(&raw))
	var plan []struct {
		Plan struct {
			SharedHit   int64 `json:"Shared Hit Blocks"`
			SharedRead  int64 `json:"Shared Read Blocks"`
			TempWritten int64 `json:"Temp Written Blocks"`
		}
	}
	require.NoError(t, json.Unmarshal(raw, &plan))
	require.Len(t, plan, 1)
	// Count buffer accesses rather than wall time or a particular plan node.
	// The measured regression took over 70k shared hits and spilled a sort.
	require.Less(t, plan[0].Plan.SharedHit+plan[0].Plan.SharedRead, int64(1024), "one due row should use bounded index work")
	require.Zero(t, plan[0].Plan.TempWritten, "selecting one due update should not spill")
}
