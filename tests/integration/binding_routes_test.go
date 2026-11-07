//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
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
	"hello/internal/installation"
)

func approveFixtureBindings(t *testing.T, ctx context.Context, pool *pgxpool.Pool, app int64) {
	t.Helper()
	seed, err := os.ReadFile("../../scripts/fixtures/active-binding-routes.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seed), app)
	require.NoError(t, err)
}

func TestBindingRouteApprovalFencesAttemptsAndSnapshots(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	f := readySubmission(t, ctx)
	schema := strings.Split(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	role := fmt.Sprintf("binding_route_%d", time.Now().UnixNano())
	login := pgx.Identifier{role}.Sanitize()
	executionSQL(t, ctx, f, "CREATE ROLE "+login+" LOGIN PASSWORD 'fixture-only'")
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := f.pool.Exec(cleanup, "DROP OWNED BY "+login+"; DROP ROLE "+login)
		require.NoError(t, err)
	})
	executionSQL(t, ctx, f, "GRANT USAGE ON SCHEMA "+pgx.Identifier{schema}.Sanitize()+" TO "+login)
	cfg := f.pool.Config()
	cfg.ConnConfig.User, cfg.ConnConfig.Password = role, "fixture-only"
	operator, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer operator.Close()
	call := func(id, project pgtype.UUID) (int64, error) {
		var rev int64
		err := operator.QueryRow(ctx, "SELECT apply_binding_route($1,$2)", id, project).Scan(&rev)
		return rev, err
	}
	var binding int64
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT binding_id FROM notification_delivery_intents WHERE id=$1", f.deliveries[0]).Scan(&binding))
	grant := func(binding, expected int64, state, reason string, resume *int64) pgtype.UUID {
		t.Helper()
		var id pgtype.UUID
		require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO binding_route_grants(id,application_id,binding_id,operator_role,origin_epoch,source_epoch,
            expected_revision,destination,cohort_id,evidence_id,reason,resume_from,expires_at)
            SELECT gen_random_uuid(),$1,$2,$3::regrole::oid,origin_epoch,source_epoch,$4,$5,'7a000000-0000-4000-8000-000000000001',
            gen_random_uuid(),$6,$7,clock_timestamp()+interval '1 hour' FROM notification_runtime RETURNING id`, f.app, binding, role, expected, state, reason, resume).Scan(&id))
		return id
	}
	hold := grant(binding, 1, "HELD", "incident", nil)
	_, err = call(hold, f.project)
	requireRetentionDenied(t, err)
	executionSQL(t, ctx, f, "GRANT EXECUTE ON FUNCTION apply_binding_route(uuid,uuid) TO "+login)
	_, err = operator.Exec(ctx, "UPDATE binding_route_grants SET applied_at=clock_timestamp()")
	requireRetentionDenied(t, err)
	executionSQL(t, ctx, f, "GRANT SELECT(id),UPDATE(applied_at) ON binding_route_grants TO "+login)
	_, err = operator.Exec(ctx, "UPDATE binding_route_grants SET applied_at=clock_timestamp() WHERE id=$1", hold)
	requireRetentionDenied(t, err)
	executionSQL(t, ctx, f, "REVOKE SELECT(id),UPDATE(applied_at) ON binding_route_grants FROM "+login)
	_, err = call(hold, replayUUID(t, ctx, f.pool))
	requireRetentionDenied(t, err)

	worker := executionService(t, f.pool)
	lease, err := worker.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	r, err := worker.Reserve(ctx, lease, f.deliveries[0])
	require.NoError(t, err)
	rev, err := call(hold, f.project)
	require.NoError(t, err)
	require.EqualValues(t, 2, rev)
	_, err = call(grant(binding, 1, "FCM_ACTIVE", "expansion", nil), f.project)
	require.ErrorContains(t, err, "revision conflict")
	_, err = worker.Reserve(ctx, lease, f.deliveries[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)

	// A new activation permits new snapshots. Only explicit revalidation
	// may allow an older pending audience to obtain a fresh reservation.
	_, err = call(grant(binding, 2, "FCM_ACTIVE", "expansion", nil), f.project)
	require.NoError(t, err)
	_, err = worker.Reserve(ctx, lease, f.deliveries[0])
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
	one := int64(1)
	_, err = call(grant(binding, 3, "FCM_ACTIVE", "revalidation", &one), f.project)
	require.NoError(t, err)
	c := submissionClient(t, f, func(context.Context) (*auth.Token, error) {
		t.Error("stale route reservation reached credentials")
		return nil, dispatch.ErrExecutionHeld
	})
	permit, err := c.Acquire(ctx)
	require.NoError(t, err)
	_, err = submissionSender(t, worker, c).SubmitReserved(ctx, r, permit)
	require.ErrorIs(t, err, dispatch.ErrExecutionHeld)
	require.Equal(t, 1, executionAttempts(t, ctx, f))
	require.NoError(t, worker.Release(ctx, lease))
	executionSQL(t, ctx, f, "UPDATE notification_delivery_intents SET next_attempt_at=clock_timestamp()-interval '1 second'")
	lease, err = worker.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	resumed, err := worker.Reserve(ctx, lease, f.deliveries[0])
	require.NoError(t, err)

	route := installation.Route{Namespace: "execution-fixture", Key: "route", Platform: "IOS", AddressType: installation.FCMRegistrationToken}
	_, err = f.lifecycle.Enroll(ctx, installation.Principal{ApplicationID: f.app, UserID: f.user}, route, "new-enrolled-only")
	require.NoError(t, err)
	var newBinding int64
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT id FROM installation_bindings WHERE platform='IOS'").Scan(&newBinding))
	checkReady := func(want bool) {
		t.Helper()
		ok, err := f.q.CampaignAcceptanceReady(ctx, sqlc.CampaignAcceptanceReadyParams{ApplicationID: f.app, CampaignID: f.campaign})
		require.NoError(t, err)
		require.Equal(t, want, ok)
	}
	checkReady(false)
	enable := grant(newBinding, 0, "FCM_ACTIVE", "pilot", nil)
	executionSQL(t, ctx, f, "UPDATE notification_runtime SET sealed=true")
	_, err = call(enable, f.project)
	requireRetentionDenied(t, err)
	executionSQL(t, ctx, f, "UPDATE notification_runtime SET sealed=false")
	_, err = call(enable, f.project)
	require.NoError(t, err)
	checkReady(true)
	rev, err = call(hold, f.project)
	require.NoError(t, err)
	require.EqualValues(t, 2, rev)
	_, err = call(grant(newBinding, 1, "RETIRED", "retirement", nil), f.project)
	require.NoError(t, err)
	_, err = call(grant(newBinding, 2, "FCM_ACTIVE", "pilot", nil), f.project)
	require.ErrorContains(t, err, "revision conflict")
	checkReady(false)
	// The second authorization runs after OAuth has completed, immediately
	// before physical I/O. Preserve the charged attempt across this hold.
	interrupted := submissionClient(t, f, func(ctx context.Context) (*auth.Token, error) {
		_, err := call(grant(binding, 4, "HELD", "incident", nil), f.project)
		require.NoError(t, err)
		return submissionAccess(ctx)
	})
	permit, err = interrupted.Acquire(ctx)
	require.NoError(t, err)
	report, err := submissionSender(t, worker, interrupted).SubmitReserved(ctx, resumed, permit)
	require.Error(t, err)
	require.NotNil(t, report)
	outcome, ok := report.Outcome()
	require.True(t, ok)
	require.Equal(t, fcm.NotSubmitted, outcome.Disposition)
	require.Zero(t, outcome.PhysicalRequests)
	require.Equal(t, 2, executionAttempts(t, ctx, f))
}
