//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"hello/internal/dispatch"
	"hello/internal/installation"
	"hello/internal/kafka"
)

type resultCommitDB struct {
	*pgxpool.Pool
	uncertain atomic.Bool
}

func (d *resultCommitDB) BeginTx(ctx context.Context, o pgx.TxOptions) (pgx.Tx, error) {
	tx, err := d.Pool.BeginTx(ctx, o)
	if err != nil {
		return nil, err
	}
	return resultCommitTx{Tx: tx, db: d}, nil
}

type resultCommitTx struct {
	pgx.Tx
	db *resultCommitDB
}

func (tx resultCommitTx) Commit(ctx context.Context) error {
	if err := tx.Tx.Commit(ctx); err != nil {
		return err
	}
	if tx.db.uncertain.Swap(false) {
		return errors.New("lost result acknowledgement")
	}
	return nil
}

type resultFixture struct {
	executionFixture
	service  *dispatch.Execution
	lease    *dispatch.ExecutionLease
	delivery pgtype.UUID
	result   *dispatch.SubmissionResult
	db       *resultCommitDB
}

func newResultFixture(t *testing.T, ctx context.Context, address string) resultFixture {
	t.Helper()
	f := resultFixture{executionFixture: newExecutionFixture(t, ctx, address)}
	executionSQL(t, ctx, f.executionFixture, `UPDATE notifications SET payload='{"fixture":"integration"}'::jsonb`)
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT d.id FROM notification_delivery_intents d JOIN mobile_installations i ON i.id=d.installation_id WHERE i.public_id=$1`, f.enrollment.State.PublicID).Scan(&f.delivery))
	f.db = &resultCommitDB{Pool: f.pool}
	f.service = executionService(t, f.db)
	var err error
	f.lease, err = f.service.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	client := submissionClient(t, f.executionFixture, submissionAccess)
	f.result, err = submissionSender(t, f.service, client).Send(ctx, f.lease, f.delivery)
	require.NoError(t, err)
	require.NotNil(t, f.result)
	return f
}
func (f resultFixture) state(t *testing.T, ctx context.Context) (string, int64, int) {
	t.Helper()
	var status string
	var version int64
	var events int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status,delivery_version FROM notification_delivery_intents WHERE id=$1`, f.delivery).Scan(&status, &version))
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM notification_update_outbox`).Scan(&events))
	return status, version, events
}
func (f resultFixture) event(t *testing.T, ctx context.Context) (kafka.ScopedDeliveryUpdated, []byte) {
	t.Helper()
	var raw []byte
	var event kafka.ScopedDeliveryUpdated
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT payload FROM notification_update_outbox WHERE delivery_id=$1`, f.delivery).Scan(&raw))
	require.NoError(t, json.Unmarshal(raw, &event))
	require.NoError(t, event.Validate())
	require.NotContains(t, string(raw), "receipt_token")
	require.NotContains(t, string(raw), "execution-address")
	return event, raw
}

func TestDeliveryResultAtomicAndIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newResultFixture(t, ctx, "execution-address-0")
	recorded, err := f.service.RecordResult(ctx, f.result)
	require.NoError(t, err)
	require.True(t, recorded.ProjectionChanged)
	require.EqualValues(t, 1, recorded.DeliveryVersion)
	status, version, count := f.state(t, ctx)
	require.Equal(t, "SENT", status)
	require.EqualValues(t, 1, version)
	require.Equal(t, 1, count)
	event, original := f.event(t, ctx)
	require.Equal(t, f.project.String(), event.ApplicationID)
	require.Equal(t, f.delivery.String(), event.DeliveryID)
	require.NotNil(t, event.ProviderAcceptedAt)
	require.False(t, event.ObservedDelivered)
	recorded, err = f.service.RecordResult(ctx, f.result)
	require.NoError(t, err)
	require.True(t, recorded.AlreadyRecorded)
	_, after := f.event(t, ctx)
	require.True(t, bytes.Equal(original, after))
	_, err = f.pool.Exec(ctx, `UPDATE notification_update_outbox SET payload='changed'::bytea`)
	require.Error(t, err)
	executionSQL(t, ctx, f.executionFixture, `UPDATE notification_update_outbox SET attempts=attempts+1`)
	// Purging a dispatched row must not recreate it on duplicate result persistence.
	executionSQL(t, ctx, f.executionFixture, `DELETE FROM notification_update_outbox`)
	recorded, err = f.service.RecordResult(ctx, f.result)
	require.NoError(t, err)
	require.True(t, recorded.AlreadyRecorded)
	_, _, count = f.state(t, ctx)
	require.Zero(t, count)
}
func TestDeliveryResultUnknownCommitAndOutboxRollback(t *testing.T) {
	for _, mode := range []string{"lost acknowledgement", "outbox rejection"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			f := newResultFixture(t, ctx, "fixture-unregistered")
			if mode == "lost acknowledgement" {
				f.db.uncertain.Store(true)
			} else {
				executionSQL(t, ctx, f.executionFixture, `CREATE FUNCTION reject_result_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture failure'; END; $$;
    CREATE TRIGGER reject_result_event BEFORE INSERT ON notification_update_outbox FOR EACH ROW EXECUTE FUNCTION reject_result_event()`)
			}
			recorded, err := f.service.RecordResult(ctx, f.result)
			require.Error(t, err)
			require.Equal(t, dispatch.RecordedResult{}, recorded)
			status, _, count := f.state(t, ctx)
			if mode == "lost acknowledgement" {
				require.Equal(t, "FAILED", status)
				require.Equal(t, 1, count)
			} else {
				require.Equal(t, "PENDING", status)
				require.Zero(t, count)
				var retired bool
				require.NoError(t, f.pool.QueryRow(ctx, `SELECT retired_at IS NOT NULL FROM installation_bindings WHERE installation_id=(SELECT installation_id FROM notification_delivery_intents WHERE id=$1)`, f.delivery).Scan(&retired))
				require.False(t, retired)
				executionSQL(t, ctx, f.executionFixture, `DROP TRIGGER reject_result_event ON notification_update_outbox; DROP FUNCTION reject_result_event()`)
			}
			recorded, err = f.service.RecordResult(ctx, f.result)
			require.NoError(t, err)
			require.Equal(t, mode == "lost acknowledgement", recorded.AlreadyRecorded)
			status, version, count := f.state(t, ctx)
			require.Equal(t, "FAILED", status)
			require.EqualValues(t, 1, version)
			require.Equal(t, 1, count)
		})
	}
}
func TestDeliveryResultRejectionRetryAndHold(t *testing.T) {
	for _, tc := range []struct {
		address, status          string
		event, invalidated, hold bool
		floor                    time.Duration
	}{
		{"fixture-unregistered", "FAILED", true, true, false, 0},
		{"fixture-payload-error", "FAILED", true, false, false, 0},
		{"fixture-sender", "FAILED", true, false, true, 0},
		{"fixture-auth", "FAILED", true, false, true, 0},
		{"fixture-apns", "FAILED", true, false, true, 0},
		{"fixture-quota", "PENDING", false, false, false, time.Minute},
		{"fixture-unavailable", "PENDING", false, false, false, 10 * time.Second},
	} {
		t.Run(tc.address, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			f := newResultFixture(t, ctx, tc.address)
			recorded, err := f.service.RecordResult(ctx, f.result)
			require.NoError(t, err)
			require.Equal(t, tc.event, recorded.ProjectionChanged)
			require.Equal(t, tc.invalidated, recorded.BindingInvalidated)
			require.Equal(t, tc.hold, recorded.DispatchHeld)
			status, _, events := f.state(t, ctx)
			require.Equal(t, tc.status, status)
			if tc.event {
				require.Equal(t, 1, events)
			} else {
				require.Zero(t, events)
			}
			var held, retired bool
			var floor float64
			require.NoError(t, f.pool.QueryRow(ctx, `SELECT hold_new FROM application_dispatch_policy WHERE application_id=$1`, f.app).Scan(&held))
			require.Equal(t, tc.hold, held)
			require.NoError(t, f.pool.QueryRow(ctx, `SELECT retired_at IS NOT NULL FROM installation_bindings WHERE installation_id=(SELECT installation_id FROM notification_delivery_intents WHERE id=$1)`, f.delivery).Scan(&retired))
			require.Equal(t, tc.invalidated, retired)
			if tc.floor > 0 {
				require.NoError(t, f.pool.QueryRow(ctx, `SELECT extract(epoch FROM next_attempt_at-attempt_resolved_at)::float8 FROM notification_delivery_intents WHERE id=$1`, f.delivery).Scan(&floor))
				require.GreaterOrEqual(t, floor, tc.floor.Seconds())
			}
		})
	}
}
func TestDeliveryResultFencesAndPreservesEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		allowed   bool
		status    string
	}{
		{"lease expired", `UPDATE notifications SET lease_until=clock_timestamp()-interval '1 second'`, false, "PENDING"},
		{"owner replaced", `UPDATE notifications SET lease_owner=gen_random_uuid(),lease_generation=lease_generation+1`, false, "PENDING"},
		{"origin replaced", `UPDATE notification_runtime SET origin_epoch=gen_random_uuid()`, false, "PENDING"},
		{"source replaced", `UPDATE notification_runtime SET source_epoch=gen_random_uuid()`, false, "PENDING"},
		{"runtime held", `UPDATE notification_runtime SET migration_hold=true`, false, "PENDING"},
		{"app held", `UPDATE applications SET status='PROVISIONING'; UPDATE application_dispatch_policy SET hold_new=true`, true, "SENT"},
		{"late accepted", `UPDATE notifications SET deadline_at=clock_timestamp()-interval '1 second'`, true, "SENT"},
		{"early opened", `UPDATE notification_delivery_intents SET status='OPENED'`, true, "OPENED"},
		{"logout after submission", `UPDATE installation_bindings SET retired_at=clock_timestamp(); UPDATE mobile_installations SET owner_user_id=NULL,owner_epoch=owner_epoch+1,version=version+1`, true, "SENT"},
		{"version overflow", `UPDATE notification_delivery_intents SET delivery_version=9223372036854775807`, false, "PENDING"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			f := newResultFixture(t, ctx, "execution-address-0")
			executionSQL(t, ctx, f.executionFixture, tc.sql)
			_, err := f.service.RecordResult(ctx, f.result)
			if tc.allowed {
				require.NoError(t, err)
				event, _ := f.event(t, ctx)
				require.Equal(t, tc.status, event.Status)
				if tc.status == "OPENED" {
					require.True(t, event.ObservedOpened)
				}
			} else {
				require.Error(t, err)
			}
			status, _, events := f.state(t, ctx)
			require.Equal(t, tc.status, status)
			if !tc.allowed {
				require.Zero(t, events)
			}
		})
	}
}
func TestDeliveryResultKeepsRefreshedBinding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newResultFixture(t, ctx, "fixture-unregistered")
	state, err := f.lifecycle.Apply(ctx, installation.Principal{ApplicationID: f.app, UserID: f.user}, installation.Change{
		InstallationID: f.enrollment.State.PublicID, Proof: f.enrollment.Proof, ExpectedVersion: f.enrollment.State.Version, Operation: installation.Refresh,
		Route: installation.Route{Namespace: "execution-fixture", Key: "route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}, Address: "refreshed-address",
	})
	require.NoError(t, err)
	recorded, err := f.service.RecordResult(ctx, f.result)
	require.NoError(t, err)
	require.False(t, recorded.BindingInvalidated)
	var active int
	var version int64
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM installation_bindings WHERE installation_id=(SELECT installation_id FROM notification_delivery_intents WHERE id=$1) AND retired_at IS NULL`, f.delivery).Scan(&active))
	require.Equal(t, 1, active)
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT version FROM mobile_installations WHERE public_id=$1`, f.enrollment.State.PublicID).Scan(&version))
	require.Equal(t, state.Version, version)
}
func TestDeliveryResultDownRefusesRetainedEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	f := newResultFixture(t, ctx, "execution-address-0")
	_, err := f.service.RecordResult(ctx, f.result)
	require.NoError(t, err)
	data, err := os.ReadFile("../../db/migrations/000030_create_delivery_results.down.sql")
	require.NoError(t, err)
	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	// The migration targets public in deployment; this fixture owns only its schema.
	down := strings.ReplaceAll(string(data), "public.", pgx.Identifier{schema}.Sanitize()+".")
	_, err = f.pool.Exec(ctx, down)
	require.Error(t, err)
	status, _, events := f.state(t, ctx)
	require.Equal(t, "SENT", status)
	require.Equal(t, 1, events)
}

func TestDeliveryResultBudgetDeadlineAndAttemptFence(t *testing.T) {
	for _, mode := range []string{"budget", "deadline", "new attempt"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			f := newResultFixture(t, ctx, "fixture-unavailable")
			switch mode {
			case "budget":
				executionSQL(t, ctx, f.executionFixture, `UPDATE notification_delivery_intents SET attempt_count=3 WHERE id=$1`, f.delivery)
			case "deadline":
				executionSQL(t, ctx, f.executionFixture, `UPDATE notifications SET deadline_at=clock_timestamp()-interval '1 second'`)
			}
			_, err := f.service.RecordResult(ctx, f.result)
			require.NoError(t, err)
			if mode != "new attempt" {
				event, _ := f.event(t, ctx)
				require.Equal(t, "FAILED", event.Status)
				require.NotNil(t, event.Reason)
				reason := "retry_budget_exhausted"
				if mode == "deadline" {
					reason = "ttl_expired"
				}
				require.Equal(t, reason, *event.Reason)
			} else {
				executionSQL(t, ctx, f.executionFixture, `UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1`, f.delivery)
				_, err = f.service.Reserve(ctx, f.lease, f.delivery)
				require.NoError(t, err)
				_, err = f.service.RecordResult(ctx, f.result)
				require.ErrorIs(t, err, dispatch.ErrExecutionUnavailable)
				status, version, events := f.state(t, ctx)
				require.Equal(t, "PENDING", status)
				require.Zero(t, version)
				require.Zero(t, events)
			}
		})
	}
}
