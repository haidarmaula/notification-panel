//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"hello/internal/database/sqlc"
	"hello/internal/dispatch"
	"hello/internal/fcm"
	"hello/internal/features/applications"
	"hello/internal/kafka"
	"hello/internal/middleware"
	"hello/internal/token"
)

const pilotCohort = "7a000000-0000-4000-8000-000000000001"

func dispatchApprovers(t *testing.T, f executionFixture) func(string) {
	t.Helper()
	ctx := t.Context()
	schema := pgx.Identifier{strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]}.Sanitize()
	// This scratch application already reached ACTIVE in the execution fixture.
	// Reproduce the finalized ownership columns and the real lifecycle guard.
	for _, table := range []string{"users", "device_tokens", "templates", "segments", "segment_members", "notifications",
		"upload_batches", "upload_batch_rows", "notification_targets", "notification_deliveries", "notification_reads"} {
		executionSQL(t, ctx, f, "ALTER TABLE "+pgx.Identifier{table}.Sanitize()+" ALTER COLUMN application_id SET NOT NULL")
	}
	executionSQL(t, ctx, f, `ALTER TABLE applications ADD CONSTRAINT application_lifecycle_status CHECK(status IN ('PROVISIONING','ACTIVE','HELD','RETIRED'));
CREATE TRIGGER guard_application_lifecycle BEFORE INSERT OR UPDATE ON applications FOR EACH ROW EXECUTE FUNCTION guard_application_lifecycle();`)
	roles := map[string]*pgxpool.Pool{}
	names := map[string]string{}
	for _, purpose := range []string{"issuer", "operator"} {
		group := "hello_dispatch_" + purpose
		var existing bool
		require.NoError(t, f.pool.QueryRow(ctx, "SELECT to_regrole($1) IS NOT NULL", group).Scan(&existing))
		if !existing {
			executionSQL(t, ctx, f, "CREATE ROLE "+group+" NOLOGIN")
			t.Cleanup(func() { _, err := f.pool.Exec(context.Background(), "DROP ROLE "+group); require.NoError(t, err) })
		}
		name := fmt.Sprintf("pilot_%s_%d", purpose, time.Now().UnixNano())
		login := pgx.Identifier{name}.Sanitize()
		executionSQL(t, ctx, f, "CREATE ROLE "+login+" LOGIN PASSWORD 'fixture-only'")
		t.Cleanup(func() {
			_, err := f.pool.Exec(context.Background(), "DROP OWNED BY "+login+"; DROP ROLE "+login)
			require.NoError(t, err)
		})
		executionSQL(t, ctx, f, "GRANT "+group+" TO "+login+"; GRANT USAGE ON SCHEMA "+schema+" TO "+login)
		if purpose == "issuer" {
			executionSQL(t, ctx, f, "GRANT INSERT ON application_dispatch_grants TO "+login)
		} else {
			executionSQL(t, ctx, f, "GRANT EXECUTE ON FUNCTION apply_application_dispatch(uuid,uuid) TO "+login)
		}
		cfg := f.pool.Config()
		cfg.ConnConfig.User, cfg.ConnConfig.Password = name, "fixture-only"
		member, err := pgxpool.NewWithConfig(ctx, cfg)
		require.NoError(t, err)
		t.Cleanup(member.Close)
		roles[purpose], names[purpose] = member, name
	}
	return func(mode string) {
		t.Helper()
		var appRev, policyRev int64
		var prev string
		var origin, source pgtype.UUID
		require.NoError(t, f.pool.QueryRow(ctx, `SELECT a.revision,p.dispatch_revision,p.dispatch_mode,r.origin_epoch,r.source_epoch
FROM applications a JOIN application_dispatch_policy p ON p.application_id=a.id CROSS JOIN notification_runtime r WHERE a.id=$1`, f.app).Scan(&appRev, &policyRev, &prev, &origin, &source))
		var cohort *string
		if mode != "HELD" {
			v := pilotCohort
			cohort = &v
		}
		reason := map[string]string{"PILOT": "pilot", "HELD": "incident", "FULL": "full_coverage"}[mode]
		id := replayUUID(t, ctx, f.pool)
		_, err := roles["issuer"].Exec(ctx, `INSERT INTO application_dispatch_grants(id,project_id,operator_role,application_revision,expected_revision,previous_mode,
destination,cohort_id,origin_epoch,source_epoch,reason,legacy_evidence,consent_evidence,coverage_evidence,quota_evidence,expires_at)
VALUES($1,$2,$3::regrole::oid,$4,$5,$6,$7,$8::uuid,$9,$10,$11,sha256('legacy'::bytea),sha256('consent'::bytea),sha256('coverage'::bytea),sha256('quota'::bytea),clock_timestamp()+interval '30 minutes')`, id, f.project, names["operator"], appRev, policyRev, prev, mode, cohort, origin, source, reason)
		require.NoError(t, err)
		var applied int64
		require.NoError(t, roles["operator"].QueryRow(ctx, "SELECT apply_application_dispatch($1,$2)", id, f.project).Scan(&applied))
		require.Equal(t, policyRev+1, applied)
	}
}

func TestManagedDispatchPilotScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	f := readySubmission(t, ctx)
	approve := dispatchApprovers(t, f)
	service := applications.NewService(f.q, f.pool)
	actor := context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: f.staff})
	prep, err := dispatch.NewPreparer(f.pool)
	require.NoError(t, err)
	var segment, empty int64
	for _, entry := range []struct {
		name string
		id   *int64
	}{{"Pilot users", &segment}, {"Empty pilot", &empty}} {
		require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO segments(application_id,name,created_by) VALUES($1,$2,$3) RETURNING id", f.app, entry.name, f.staff).Scan(entry.id))
	}
	executionSQL(t, ctx, f, "INSERT INTO segment_members(application_id,segment_id,user_id) VALUES($1,$2,$3)", f.app, segment, f.user)
	draft := func(kind string, segmentID int64) int64 {
		t.Helper()
		var id int64
		require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notifications(application_id,title,body,payload,created_by) VALUES($1,'Pilot','Test','{"fixture":"integration"}'::jsonb,$2) RETURNING id`, f.app, f.staff).Scan(&id))
		var user, seg any
		if kind == "INDIVIDUAL" {
			user = f.user
		}
		if kind == "SEGMENT" {
			seg = segmentID
		}
		executionSQL(t, ctx, f, "INSERT INTO notification_targets(application_id,notification_id,target_type,user_id,segment_id) VALUES($1,$2,$3,$4,$5)", f.app, id, kind, user, seg)
		return id
	}
	accept := func(id int64) (kafka.ScopedSendRequested, error) {
		event := kafka.ScopedSendRequested{SchemaVersion: 1, ApplicationID: f.project.String(), NotificationID: id}
		_, _, err := service.AcceptCampaign(actor, f.project, id, "")
		if err == nil {
			err = f.pool.QueryRow(ctx, "SELECT origin_epoch::text,source_epoch::text,queued_at,deadline_at FROM notifications WHERE id=$1", id).Scan(&event.OriginEpoch, &event.SourceEpoch, &event.RequestedAt, &event.DeadlineAt)
		}
		return event, err
	}
	individual := draft("INDIVIDUAL", 0)
	_, err = accept(individual)
	require.Error(t, err, "existing open flags must not release a managed project")
	approve("PILOT")
	var rawHold, legacyCoverage bool
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT hold_new,coverage_approved FROM application_dispatch_policy WHERE application_id=$1", f.app).Scan(&rawHold, &legacyCoverage))
	require.False(t, rawHold)
	require.False(t, legacyCoverage, "old clients must not interpret a pilot as authority for a full audience")
	broadcast := draft("BROADCAST", 0)
	_, err = accept(broadcast)
	require.Error(t, err)
	_, err = service.ScheduleCampaign(actor, f.project, broadcast, time.Now().Add(time.Hour))
	require.Error(t, err)
	_, err = accept(draft("SEGMENT", empty))
	require.Error(t, err)
	worker := executionService(t, f.pool)
	lease, err := worker.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	_, err = worker.Reserve(ctx, lease, f.deliveries[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld, "previously prepared broadcast cannot send in pilot")
	require.NoError(t, worker.Release(ctx, lease))
	event, err := accept(individual)
	require.NoError(t, err)
	snapshot, err := prep.Prepare(ctx, event)
	require.NoError(t, err)
	require.EqualValues(t, 2, snapshot.EligibleDevices)
	due, err := f.q.ListExecutionRecoveryCampaigns(ctx, sqlc.ListExecutionRecoveryCampaignsParams{ApplicationPublicID: f.project, PageSize: 100, MaxAttempts: 3})
	require.NoError(t, err)
	var dueIDs []int64
	for _, row := range due {
		dueIDs = append(dueIDs, row.ID)
	}
	require.Contains(t, dueIDs, individual)
	require.NotContains(t, dueIDs, f.campaign, "pre-pilot audiences are not ordinary pilot work")
	queued, err := accept(draft("SEGMENT", segment))
	require.NoError(t, err)
	lease, err = worker.Acquire(ctx, f.project, individual)
	require.NoError(t, err)
	var delivery pgtype.UUID
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT id FROM notification_delivery_intents WHERE notification_id=$1 ORDER BY id LIMIT 1", individual).Scan(&delivery))
	reservation, err := worker.Reserve(ctx, lease, delivery)
	require.NoError(t, err)
	interrupted := submissionClient(t, f, func(ctx context.Context) (*auth.Token, error) {
		approve("HELD")
		approve("PILOT")
		return submissionAccess(ctx)
	})
	permit, err := interrupted.Acquire(ctx)
	require.NoError(t, err)
	report, err := submissionSender(t, worker, interrupted).SubmitReserved(ctx, reservation, permit)
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
	require.NotNil(t, report)
	outcome, ok := report.Outcome()
	require.True(t, ok)
	require.Equal(t, fcm.NotSubmitted, outcome.Disposition)
	require.Zero(t, outcome.PhysicalRequests)
	// Reassign only one device to a different approved FCM cohort. Route approval
	// alone cannot permit a partial pilot snapshot for the same targeted user.
	executionSQL(t, ctx, f, `INSERT INTO binding_route_grants(id,application_id,binding_id,operator_role,origin_epoch,source_epoch,
expected_revision,destination,cohort_id,evidence_id,reason,issued_at,expires_at,applied_at)
SELECT gen_random_uuid(),application_id,binding_id,operator_role,origin_epoch,source_epoch,1,'FCM_ACTIVE',
'7a000000-0000-4000-8000-000000000099',gen_random_uuid(),'expansion',statement_timestamp(),statement_timestamp()+interval '1 hour',statement_timestamp()
FROM binding_route_grants WHERE application_id=$1 ORDER BY binding_id LIMIT 1`, f.app)
	_, err = prep.Prepare(ctx, queued)
	require.ErrorIs(t, err, dispatch.ErrAdmissionHeld)
	for _, kind := range []string{"INDIVIDUAL", "SEGMENT"} {
		_, err = accept(draft(kind, segment))
		require.Error(t, err)
	}
	var intents, markers int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM notification_delivery_intents WHERE notification_id=$1),
(SELECT count(*) FROM notification_preparations WHERE notification_id=$1)`, queued.NotificationID).Scan(&intents, &markers))
	require.Zero(t, intents)
	require.Zero(t, markers)
	approve("FULL")
	all, err := accept(broadcast)
	require.NoError(t, err)
	full, err := prep.Prepare(ctx, all)
	require.NoError(t, err)
	require.EqualValues(t, 2, full.EligibleDevices)
	// Even an individually targeted FULL snapshot cannot send a matching subset
	// after a switch to PILOT. Its complete audience never passed a pilot check.
	mixed, err := accept(draft("INDIVIDUAL", 0))
	require.NoError(t, err)
	full, err = prep.Prepare(ctx, mixed)
	require.NoError(t, err)
	require.EqualValues(t, 2, full.EligibleDevices)
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT d.id FROM notification_delivery_intents d JOIN current_binding_routes r
ON (r.application_id,r.binding_id)=(d.application_id,d.binding_id) WHERE d.notification_id=$1 AND r.cohort_id=$2`, mixed.NotificationID, pilotCohort).Scan(&delivery))
	approve("HELD")
	approve("PILOT")
	lease, err = worker.Acquire(ctx, f.project, mixed.NotificationID)
	require.NoError(t, err)
	_, err = worker.Reserve(ctx, lease, delivery)
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
	var charges int
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT sum(attempt_count) FROM notification_delivery_intents WHERE notification_id=$1", mixed.NotificationID).Scan(&charges))
	require.Zero(t, charges)
	executionSQL(t, ctx, f, "UPDATE notification_runtime SET source_epoch=gen_random_uuid() WHERE singleton")
	policy, err := f.q.GetApplicationDispatchPolicy(ctx, f.app)
	require.NoError(t, err)
	require.True(t, policy.HoldNew)
	require.False(t, policy.CoverageApproved)
}
