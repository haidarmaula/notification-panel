//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
	"hello/internal/dispatch"
	"hello/internal/kafka"
)

func (f replayRegistrationFixture) acceptedEvent(t *testing.T, ctx context.Context) kafka.ScopedSendRequested {
	t.Helper()
	_, err := f.pool.Exec(ctx, `UPDATE notification_runtime SET migration_hold=false;
 INSERT INTO application_dispatch_policy(application_id,hold_new,coverage_approved,push_ttl_seconds,key_ttl_seconds)
 SELECT id,false,true,300,300 FROM applications;
 UPDATE notifications SET status='QUEUED',queued_at=clock_timestamp(),deadline_at=clock_timestamp()+interval '5 minutes',
 origin_epoch=(SELECT origin_epoch FROM notification_runtime),source_epoch=(SELECT source_epoch FROM notification_runtime)`)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, "INSERT INTO notification_targets(application_id,notification_id,target_type) VALUES($1,$2,'BROADCAST')", f.app, f.campaign)
	require.NoError(t, err)
	event := kafka.ScopedSendRequested{SchemaVersion: 1, ApplicationID: f.project.String(), NotificationID: f.campaign}
	var origin, source pgtype.UUID
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT queued_at,deadline_at,origin_epoch,source_epoch FROM notifications WHERE id=$1", f.campaign).Scan(&event.RequestedAt, &event.DeadlineAt, &origin, &source))
	event.OriginEpoch, event.SourceEpoch = origin.String(), source.String()
	return event
}

func (f replayRegistrationFixture) quarantineEvent(t *testing.T, ctx context.Context, event kafka.ScopedSendRequested) {
	t.Helper()
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	store, err := dispatch.NewQuarantine(f.pool)
	require.NoError(t, err)
	require.NoError(t, store.Store(ctx, dispatch.SourceMessage{LogSourceID: f.source, Topic: "send", Partition: 0, Offset: 42, Key: []byte("notification_" + strconv.FormatInt(event.NotificationID, 10)), Payload: payload}, dispatch.AdmissionExhausted, 3))
}

func TestReplayInspectionEligibilityIsReadOnly(t *testing.T) {
	cases := []struct {
		name, sql string
		want      dispatch.ReplayDisposition
	}{
		{name: "queued eligible", want: dispatch.ReplayEligible},
		{name: "global hold", sql: "UPDATE notification_runtime SET migration_hold=true", want: dispatch.ReplayOnHold},
		{name: "policy hold", sql: "UPDATE application_dispatch_policy SET hold_new=true", want: dispatch.ReplayOnHold},
		{name: "coverage hold", sql: "UPDATE application_dispatch_policy SET coverage_approved=false", want: dispatch.ReplayOnHold},
		{name: "retry not due", sql: "UPDATE notifications SET preparation_next_attempt_at=clock_timestamp()+interval '1 minute'", want: dispatch.ReplayOnHold},
		{name: "no supported targets", sql: "DELETE FROM notification_targets", want: dispatch.ReplayRejected},
		{name: "unprepared processing", sql: "UPDATE notifications SET status='PROCESSING'", want: dispatch.ReplayReconciliation},
		{name: "terminal", sql: "UPDATE notifications SET status='FAILED'", want: dispatch.ReplayTerminal},
		{name: "held app queued", sql: "UPDATE applications SET status='HELD'", want: dispatch.ReplayOnHold},
		{name: "held app terminal", sql: "UPDATE applications SET status='HELD'; UPDATE notifications SET status='FAILED'", want: dispatch.ReplayTerminal},
		{name: "held app prepared", sql: `UPDATE applications SET status='HELD'; UPDATE notifications SET status='PROCESSING'; INSERT INTO notification_preparations(application_id,notification_id,origin_epoch,source_epoch,resolved_user_count,eligible_user_count,eligible_device_count) SELECT application_id,id,origin_epoch,source_epoch,0,0,0 FROM notifications`, want: dispatch.ReplayPrepared},

		{name: "prepared empty snapshot", sql: `UPDATE notifications SET status='COMPLETED'; INSERT INTO notification_preparations(application_id,notification_id,origin_epoch,source_epoch,resolved_user_count,eligible_user_count,eligible_device_count) SELECT application_id,id,origin_epoch,source_epoch,0,0,0 FROM notifications`, want: dispatch.ReplayPrepared},
		{name: "queued inconsistent marker", sql: `INSERT INTO notification_preparations(application_id,notification_id,origin_epoch,source_epoch,resolved_user_count,eligible_user_count,eligible_device_count) SELECT application_id,id,origin_epoch,source_epoch,0,0,0 FROM notifications`, want: dispatch.ReplayReconciliation},
		{name: "marker wrong epoch", sql: `UPDATE notifications SET status='PROCESSING'; INSERT INTO notification_preparations(application_id,notification_id,origin_epoch,source_epoch,resolved_user_count,eligible_user_count,eligible_device_count) SELECT application_id,id,gen_random_uuid(),source_epoch,0,0,0 FROM notifications`, want: dispatch.ReplayReconciliation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f := newReplayRegistrationFixture(t, ctx)
			event := f.acceptedEvent(t, ctx)
			f.quarantineEvent(t, ctx, event)
			request := f.input(t, ctx)
			credential, _ := f.credential(t, ctx, request)
			if tc.sql != "" {
				_, err := f.pool.Exec(ctx, tc.sql)
				require.NoError(t, err)
			}
			var before string
			require.NoError(t, f.pool.QueryRow(ctx, "SELECT status FROM notifications WHERE id=$1", f.campaign).Scan(&before))
			result, err := f.service.Inspect(ctx, request, credential, request.Positions[0])
			require.NoError(t, err)
			require.Equal(t, tc.want, result.Disposition)
			var after string
			require.NoError(t, f.pool.QueryRow(ctx, "SELECT status FROM notifications WHERE id=$1", f.campaign).Scan(&after))
			require.Equal(t, before, after)
			for _, table := range []string{"notification_replay_requests", "notification_replay_attempts", "notification_replay_controls", "notification_delivery_intents", "audit_logs"} {
				var n int
				require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n))
				require.Zero(t, n, "dry run mutated %s", table)
			}
		})
	}
}

func TestReplayInspectionRejectsScopeAndAcceptanceMismatch(t *testing.T) {
	for _, kind := range []string{"cross app", "missing evidence", "wrong acceptance", "stale lineage", "malformed", "expired"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f := newReplayRegistrationFixture(t, ctx)
			event := f.acceptedEvent(t, ctx)
			switch kind {
			case "cross app":
				event.ApplicationID = replayUUID(t, ctx, f.pool).String()
			case "stale lineage":
				event.SourceEpoch = replayUUID(t, ctx, f.pool).String()
			case "wrong acceptance":
				event.RequestedAt = event.RequestedAt.Add(-time.Second)
			case "expired":
				require.NoError(t, f.pool.QueryRow(ctx, "UPDATE notifications SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1 RETURNING deadline_at", f.campaign).Scan(&event.DeadlineAt))
			}
			if kind == "malformed" {
				store, err := dispatch.NewQuarantine(f.pool)
				require.NoError(t, err)
				require.NoError(t, store.Store(ctx, dispatch.SourceMessage{LogSourceID: f.source, Topic: "send", Partition: 0, Offset: 42, Key: []byte("private malformed key"), Payload: []byte("private malformed payload")}, dispatch.InvalidEnvelope, 1))
			} else if kind != "missing evidence" {
				f.quarantineEvent(t, ctx, event)
			}
			request := f.input(t, ctx)
			credential, _ := f.credential(t, ctx, request)
			result, err := f.service.Inspect(ctx, request, credential, request.Positions[0])
			switch kind {
			case "cross app", "missing evidence":
				require.ErrorIs(t, err, dispatch.ErrReplayDenied)
			case "wrong acceptance", "stale lineage", "malformed":
				require.NoError(t, err)
				require.Equal(t, dispatch.ReplayRejected, result.Disposition)
				require.Zero(t, result.NotificationID)
			case "expired":
				require.NoError(t, err)
				require.Equal(t, dispatch.ReplayExpired, result.Disposition)
			}
		})
	}
}

func TestReplayInspectionPreservesExistingControl(t *testing.T) {
	for _, resolved := range []bool{false, true} {
		name := "busy"
		if resolved {
			name = "resolved"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f := newReplayRegistrationFixture(t, ctx)
			event := f.acceptedEvent(t, ctx)
			f.quarantineEvent(t, ctx, event)
			otherRequest := f.request(t, ctx)
			require.NoError(t, f.q.InsertReplayControl(ctx, sqlc.InsertReplayControlParams{LogSourceID: f.source, OriginalOffset: 42}))
			var origin, source pgtype.UUID
			require.NoError(t, origin.Scan(event.OriginEpoch))
			require.NoError(t, source.Scan(event.SourceEpoch))
			attempt, err := f.q.InsertReplayAttempt(ctx, sqlc.InsertReplayAttemptParams{
				ApplicationID: f.app, RequestID: otherRequest, LogSourceID: f.source, OriginalOffset: 42,
				NotificationID: pgtype.Int8{Int64: f.campaign, Valid: true}, OriginEpoch: origin, SourceEpoch: source,
				ClaimToken: replayUUID(t, ctx, f.pool), Outcome: "started",
			})
			require.NoError(t, err)
			n, err := f.q.ClaimReplayControl(ctx, replayClaim(attempt))
			require.NoError(t, err)
			require.EqualValues(t, 1, n)
			want := dispatch.ReplayOnHold
			if resolved {
				n, err = f.q.FinishReplayAttempt(ctx, replayFinish(attempt, "published"))
				require.NoError(t, err)
				require.EqualValues(t, 1, n)
				n, err = f.q.ResolveReplayControl(ctx, sqlc.ResolveReplayControlParams{ApplicationID: pgtype.Int8{Int64: f.app, Valid: true}, RequestID: otherRequest, LogSourceID: f.source, OriginalOffset: 42, ExpectedToken: attempt.ClaimToken, Outcome: pgtype.Text{String: "published", Valid: true}})
				require.NoError(t, err)
				require.EqualValues(t, 1, n)
				want = dispatch.ReplayResolved
			}
			before, err := f.q.LockReplayControl(ctx, sqlc.LockReplayControlParams{LogSourceID: f.source, OriginalOffset: 42})
			require.NoError(t, err)
			request := f.input(t, ctx)
			credential, _ := f.credential(t, ctx, request)
			result, err := f.service.Inspect(ctx, request, credential, request.Positions[0])
			require.NoError(t, err)
			require.Equal(t, want, result.Disposition)
			after, err := f.q.LockReplayControl(ctx, sqlc.LockReplayControlParams{LogSourceID: f.source, OriginalOffset: 42})
			require.NoError(t, err)
			require.Equal(t, before, after, "dry run must not seize or reopen existing evidence")
		})
	}
}
