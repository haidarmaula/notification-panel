//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/database/sqlc"
	"hello/internal/dispatch"
	"hello/internal/installation"
	"hello/internal/kafka"
	"hello/internal/receipt"
)

type observationFixture struct {
	executionFixture
	service *dispatch.Observations
	actor   installation.Principal
	session installation.MobileSession
	report  dispatch.Observation
	scope   receipt.Scope
	signer  *receipt.Signer
	worker  *dispatch.Execution
	lease   *dispatch.ExecutionLease
	result  *dispatch.SubmissionResult
}

func observationSession(t *testing.T, ctx context.Context, f executionFixture, user int64, subject string) installation.MobileSession {
	t.Helper()
	mobileKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{33}, 32))
	originKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{34}, 32))
	origin, err := installation.NewSignedOrigin(f.q, "origin", "enroll", originKey.Public().(ed25519.PublicKey))
	require.NoError(t, err)
	authority, err := installation.NewMobileAuthority(f.q, map[pgtype.UUID]installation.MobilePolicy{f.project: {
		Issuer: "mobile", Audience: "fixture-app", Purpose: "access", Namespace: "fixture-subjects",
		VerificationKey: mobileKey.Public().(ed25519.PublicKey), Origin: origin,
	}})
	require.NoError(t, err)
	executionSQL(t, ctx, f, `INSERT INTO mobile_identities(application_id,identity_namespace,subject,user_id)
		VALUES($1,'fixture-subjects',$2,$3)`, f.app, subject, user)
	bearer, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "mobile", "aud": "fixture-app", "purpose": "access", "sub": subject, "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(mobileKey)
	require.NoError(t, err)
	session, err := authority.AuthenticateSession(ctx, f.project, bearer)
	require.NoError(t, err)
	return session
}

func newObservationFixture(t *testing.T, ctx context.Context, address string) observationFixture {
	t.Helper()
	f := observationFixture{executionFixture: newExecutionFixture(t, ctx, address)}
	executionSQL(t, ctx, f.executionFixture, `UPDATE notifications SET payload='{"fixture":"integration"}'::jsonb`)
	f.actor = installation.Principal{ApplicationID: f.app, UserID: f.user}
	f.session = observationSession(t, ctx, f.executionFixture, f.user, "recipient")
	f.worker = executionService(t, f.pool)
	lease, err := f.worker.Acquire(ctx, f.project, f.campaign)
	require.NoError(t, err)
	f.lease = lease
	var delivery pgtype.UUID
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT d.id FROM notification_delivery_intents d
		JOIN mobile_installations i ON i.id=d.installation_id WHERE i.public_id=$1`, f.enrollment.State.PublicID).Scan(&delivery))
	client := submissionClient(t, f.executionFixture, submissionAccess)
	permit, err := client.Acquire(ctx)
	require.NoError(t, err)
	defer permit.Release()
	reservation, err := f.worker.Reserve(ctx, lease, delivery)
	require.NoError(t, err)
	material, ok := reservation.ReceiptMaterial()
	require.True(t, ok)
	f.scope = material.Scope
	primary := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{11}, 32))
	retained := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{27}, 32))
	f.signer, err = receipt.NewSigner("fixture", map[string]ed25519.PrivateKey{"fixture-key": primary, "retained": retained})
	require.NoError(t, err)
	verifier, err := receipt.NewVerifier("fixture", map[string]ed25519.PublicKey{
		"fixture-key": primary.Public().(ed25519.PublicKey), "retained": retained.Public().(ed25519.PublicKey),
	})
	require.NoError(t, err)
	f.service, err = dispatch.NewObservations(f.pool, verifier, 120)
	require.NoError(t, err)
	capability, err := f.signer.SignMaterial(material)
	require.NoError(t, err)
	f.report = dispatch.Observation{CampaignID: f.campaign, DeliveryID: delivery,
		ClientEventID: replayUUID(t, ctx, f.pool), Event: "OPENED", Token: capability.WireValue()}
	f.result, err = submissionSender(t, f.worker, client).SubmitReserved(ctx, reservation, permit)
	require.NoError(t, err)
	require.NotNil(t, f.result)
	return f
}

func (f observationFixture) record(ctx context.Context, report dispatch.Observation) error {
	return f.service.Record(ctx, f.project, f.session, report)
}

func (f observationFixture) projections(t *testing.T, ctx context.Context) []kafka.ScopedDeliveryUpdated {
	t.Helper()
	rows, err := f.pool.Query(ctx, `SELECT payload FROM notification_update_outbox
		WHERE application_id=$1 AND delivery_id=$2 ORDER BY delivery_version`, f.app, f.report.DeliveryID)
	require.NoError(t, err)
	defer rows.Close()
	var events []kafka.ScopedDeliveryUpdated
	for rows.Next() {
		var payload []byte
		require.NoError(t, rows.Scan(&payload))
		event, err := kafka.DecodeScopedDelivery(payload)
		require.NoError(t, err)
		require.NotContains(t, string(payload), f.report.Token)
		events = append(events, event)
	}
	require.NoError(t, rows.Err())
	return events
}

func (f observationFixture) readCount(t *testing.T, ctx context.Context) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM notification_reads
		WHERE application_id=$1 AND user_id=$2 AND notification_id=$3`, f.app, f.user, f.campaign).Scan(&n))
	return n
}

func (f observationFixture) stats(t *testing.T, ctx context.Context) sqlc.GetCampaignStatisticsRow {
	t.Helper()
	row, err := f.q.GetCampaignStatistics(ctx, sqlc.GetCampaignStatisticsParams{ApplicationID: f.app, NotificationID: f.campaign})
	require.NoError(t, err)
	return row
}

func TestObservationBeforeWorkerResultAndRequestDedup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newObservationFixture(t, ctx, "execution-address-0")
	require.NoError(t, f.record(ctx, f.report))
	require.NoError(t, f.record(ctx, f.report))
	second := f.report
	second.ClientEventID = replayUUID(t, ctx, f.pool)
	require.NoError(t, f.record(ctx, second))
	second.Event = "DELIVERED"
	require.ErrorIs(t, f.record(ctx, second), dispatch.ErrObservationConflict)
	require.Equal(t, 1, f.readCount(t, ctx))

	stats := f.stats(t, ctx)
	require.True(t, stats.SnapshotComplete)
	require.EqualValues(t, 1, stats.TargetedUsers)
	require.EqualValues(t, 2, stats.TargetedDevices)
	require.EqualValues(t, 1, stats.ObservedDevices)
	require.EqualValues(t, 1, stats.OpenedDevices)
	require.EqualValues(t, 1, stats.OpenedUsers)
	require.EqualValues(t, 1, stats.PendingDevices)
	require.Zero(t, stats.ProviderAcceptedDevices, "an authenticated observation cannot invent provider acceptance")
	accepted, err := f.worker.RecordResult(ctx, f.result)
	require.NoError(t, err)
	require.EqualValues(t, 2, accepted.DeliveryVersion)
	require.EqualValues(t, 1, f.stats(t, ctx).ProviderAcceptedDevices)
	events := f.projections(t, ctx)
	require.Len(t, events, 2)
	require.Equal(t, "observation", events[0].EventKind)
	require.True(t, events[0].ObservedDelivered)
	require.True(t, events[0].ObservedOpened)
	require.Nil(t, events[0].ProviderAcceptedAt)
	require.Equal(t, "submission", events[1].EventKind)
	require.NotNil(t, events[1].ProviderAcceptedAt)
	for _, event := range events {
		require.Equal(t, "OPENED", event.Status)
	}
	executionSQL(t, ctx, f.executionFixture, "DELETE FROM notification_update_outbox")
	second = f.report
	second.ClientEventID = replayUUID(t, ctx, f.pool)
	require.NoError(t, f.record(ctx, second))
	require.Empty(t, f.projections(t, ctx))
	second.ClientEventID = replayUUID(t, ctx, f.pool)
	second.Event = "DELIVERED"
	require.NoError(t, f.record(ctx, second))
	require.NoError(t, f.record(ctx, second))
	explicit := f.projections(t, ctx)
	require.Len(t, explicit, 1)
	require.EqualValues(t, 3, explicit[0].DeliveryVersion)
	require.Equal(t, "OPENED", explicit[0].Status)
	require.NotNil(t, explicit[0].ObservedDeliveredAt)
	require.Equal(t, events[0].ObservedOpenedAt, explicit[0].ObservedOpenedAt)
	require.Equal(t, 1, f.readCount(t, ctx))
}

func TestObservationAfterFailedSubmissionRollsBackAndRetries(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newObservationFixture(t, ctx, "fixture-payload-error")
	_, err := f.worker.RecordResult(ctx, f.result)
	require.NoError(t, err)
	executionSQL(t, ctx, f.executionFixture, "UPDATE notifications SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", f.campaign)
	for _, id := range f.deliveries {
		if id != f.report.DeliveryID {
			changed, err := f.worker.RecoverDelivery(ctx, f.lease, id)
			require.NoError(t, err)
			require.True(t, changed)
		}
	}
	done, err := f.worker.FinalizeCampaign(ctx, f.lease)
	require.NoError(t, err)
	require.True(t, done)
	executionSQL(t, ctx, f.executionFixture, "ALTER TABLE notification_update_outbox ADD CONSTRAINT reject_observation CHECK (delivery_version < 2)")
	require.Error(t, f.record(ctx, f.report))
	require.Zero(t, f.readCount(t, ctx))
	require.Len(t, f.projections(t, ctx), 1)
	executionSQL(t, ctx, f.executionFixture, "ALTER TABLE notification_update_outbox DROP CONSTRAINT reject_observation")
	require.NoError(t, f.record(ctx, f.report))
	events := f.projections(t, ctx)
	require.Len(t, events, 2)
	require.Equal(t, "FAILED", events[1].Status)
	require.Equal(t, events[0].Reason, events[1].Reason)
	require.Equal(t, events[0].FailedAt, events[1].FailedAt)
	require.Nil(t, events[1].ProviderAcceptedAt)
	require.True(t, events[1].ObservedDelivered)
	require.True(t, events[1].ObservedOpened)
	stats := f.stats(t, ctx)
	require.EqualValues(t, 2, stats.SubmissionFailedDevices)
	var status string
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT status FROM notifications WHERE id=$1", f.campaign).Scan(&status))
	require.Equal(t, "FAILED", status)
	require.EqualValues(t, 1, stats.FailedObservedDevices)
	require.EqualValues(t, 1, stats.OpenedUsers)
	require.Zero(t, stats.ProviderAcceptedDevices)
	require.Equal(t, 1, f.readCount(t, ctx))
}

func TestObservationReauthorizesOriginalOwnerAndFrozenKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newObservationFixture(t, ctx, "execution-address-0")
	report := f.report
	report.Event = "DELIVERED"
	report.Token = "r1.invalid"
	require.ErrorIs(t, f.record(ctx, report), dispatch.ErrObservationNotFound)
	wrongKey, err := f.signer.Sign("retained", f.scope)
	require.NoError(t, err)
	report.Token = wrongKey.WireValue()
	require.ErrorIs(t, f.record(ctx, report), dispatch.ErrObservationNotFound)
	var other int64
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO users(application_id,external_id) VALUES($1,'other-observer') RETURNING id", f.app).Scan(&other))
	stranger := installation.Principal{ApplicationID: f.app, UserID: other}
	otherSession := observationSession(t, ctx, f.executionFixture, other, "other-recipient")
	report.Token = f.report.Token
	require.ErrorIs(t, f.service.Record(ctx, f.project, otherSession, report), dispatch.ErrObservationNotFound)
	change := installation.Change{InstallationID: f.enrollment.State.PublicID, Proof: f.enrollment.Proof,
		ExpectedVersion: f.enrollment.State.Version, Operation: installation.Refresh, Address: "refreshed-observation-address",
		Route: installation.Route{Namespace: "execution-fixture", Key: "route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}}
	state, err := f.lifecycle.Apply(ctx, f.actor, change)
	require.NoError(t, err)
	require.NoError(t, f.record(ctx, report))
	require.Zero(t, f.readCount(t, ctx), "delivery alone must leave the user unread")
	// Reassigning a verified subject after bearer parsing revokes that session,
	// even when replaying a previously acknowledged client event.
	executionSQL(t, ctx, f.executionFixture, "UPDATE mobile_identities SET user_id=$1 WHERE application_id=$2 AND subject='recipient'", other, f.app)
	require.ErrorIs(t, f.record(ctx, report), installation.ErrMobileCredential)
	executionSQL(t, ctx, f.executionFixture, "UPDATE mobile_identities SET user_id=$1 WHERE application_id=$2 AND subject='recipient'", f.user, f.app)
	change.ExpectedVersion, change.Operation, change.Address = state.Version, installation.SwitchOwner, "transferred-address"
	state, err = f.lifecycle.Apply(ctx, stranger, change)
	require.NoError(t, err)
	require.ErrorIs(t, f.record(ctx, report), dispatch.ErrObservationNotFound, "a cached result must recheck ownership")
	require.ErrorIs(t, f.service.Record(ctx, f.project, otherSession, report), dispatch.ErrObservationNotFound)
	change.ExpectedVersion, change.Address = state.Version, "returned-address"
	_, err = f.lifecycle.Apply(ctx, f.actor, change)
	require.NoError(t, err)
	require.ErrorIs(t, f.record(ctx, report), dispatch.ErrObservationNotFound)
	require.Len(t, f.projections(t, ctx), 1)
}

// Restore a historical, fully prepared and charged delivery with consistent past
// dates so expiry can be tested without changing the clock or frozen metadata.
func TestObservationExpiredOnlyForAuthenticCurrentOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newObservationFixture(t, ctx, "execution-address-0")
	var campaign int64
	var delivery pgtype.UUID
	scope := f.scope
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notifications(
		application_id,title,body,created_by,status,queued_at,deadline_at,origin_epoch,source_epoch,completed_at)
		SELECT application_id,title,body,created_by,'COMPLETED',now()-interval '3 days',now()-interval '2 days',
		       origin_epoch,source_epoch,now()-interval '2 days' FROM notifications WHERE id=$1 RETURNING id`, f.campaign).Scan(&campaign))
	executionSQL(t, ctx, f.executionFixture, `INSERT INTO notification_preparations(application_id,notification_id,
		origin_epoch,source_epoch,resolved_user_count,eligible_user_count,eligible_device_count,completed_at)
		SELECT application_id,id,origin_epoch,source_epoch,1,1,1,queued_at FROM notifications WHERE id=$1`, campaign)
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notification_delivery_intents(
		application_id,notification_id,installation_id,binding_id,owner_user_id,owner_epoch,created_at,status,
		attempt_count,attempt_id,claim_generation,last_attempt_at,attempt_resolved_at)
		SELECT application_id,$2,installation_id,binding_id,owner_user_id,owner_epoch,now()-interval '3 days','SENT',
			1,gen_random_uuid(),1,now()-interval '3 days',now()-interval '3 days' FROM notification_delivery_intents WHERE id=$1
		RETURNING id`, f.report.DeliveryID, campaign).Scan(&delivery))
	scope.NotificationID, scope.DeliveryID = campaign, delivery
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO notification_receipt_metadata(
		application_id,delivery_id,version,key_id,environment,created_at,expires_unix)
		VALUES($1,$2,1,'fixture-key','fixture',now()-interval '3 days',floor(extract(epoch FROM now()-interval '1 day')))
		RETURNING expires_unix`, f.app, delivery).Scan(&scope.ExpiresUnix))
	token, err := f.signer.Sign("fixture-key", scope)
	require.NoError(t, err)
	report := dispatch.Observation{CampaignID: campaign, DeliveryID: delivery, ClientEventID: f.report.ClientEventID, Event: "OPENED", Token: token.WireValue()}
	require.ErrorIs(t, f.record(ctx, report), receipt.ErrExpired)
	var other int64
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO users(application_id,external_id) VALUES($1,'other-observer') RETURNING id", f.app).Scan(&other))
	otherSession := observationSession(t, ctx, f.executionFixture, other, "other-recipient")
	require.ErrorIs(t, f.service.Record(ctx, f.project, otherSession, report), dispatch.ErrObservationNotFound)
	report.Token = report.Token[:len(report.Token)-4]
	require.ErrorIs(t, f.record(ctx, report), dispatch.ErrObservationNotFound)
}
