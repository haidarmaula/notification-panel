package dispatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
	"hello/internal/installation"
	"hello/internal/receipt"
)

var (
	ErrObservationInput    = errors.New("invalid device receipt")
	ErrObservationNotFound = errors.New("delivery not found")
	ErrObservationConflict = errors.New("client event ID reused")
)

// Observation is one authenticated device report. The token proves payload possession.
type Observation struct {
	CampaignID                int64
	DeliveryID, ClientEventID pgtype.UUID
	Event, Token              string
	ObservedAt                *time.Time
}

func (Observation) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (Observation) LogValue() slog.Value         { return slog.StringValue("[REDACTED]") }
func (Observation) String() string               { return "[REDACTED]" }
func (Observation) GoString() string             { return "[REDACTED]" }

// Observations records authorized facts and commits their public projection atomically.
type Observations struct {
	installationRate *receipt.RateWindow
	db               Transactor
	verifier         *receipt.Verifier
}

// NewObservations binds the ledger to the application's public-only verifier.
func NewObservations(db Transactor, verifier *receipt.Verifier, installationPerMinute int) (*Observations, error) {
	if db == nil || verifier == nil {
		return nil, ErrObservationInput
	}
	rate, err := receipt.NewRateWindow(installationPerMinute)
	if err != nil {
		return nil, err
	}
	return &Observations{db: db, verifier: verifier, installationRate: rate}, nil
}

// Record reauthorizes retries under lock, including exact duplicates.
func (s *Observations) Record(ctx context.Context, project pgtype.UUID, session installation.MobileSession, report Observation) error {
	if !executionUUID(project) || report.CampaignID <= 0 || !executionUUID(report.DeliveryID) || !executionUUID(report.ClientEventID) ||
		(report.Event != "DELIVERED" && report.Event != "OPENED") {
		return ErrObservationInput
	}
	if len(report.Token) == 0 || len(report.Token) > 256 {
		return ErrObservationNotFound
	}
	return ledgerTransaction(ctx, s.db, func(ctx context.Context, q *sqlc.Queries) error {
		locked, err := s.lock(ctx, q, project, session, report)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrObservationNotFound
		}
		if err != nil {
			return err
		}
		return report.apply(ctx, q, locked)
	})
}

type lockedObservation struct {
	source   deliverySource
	actor    installation.Principal
	received pgtype.Timestamptz
	expires  int64
}

func (s *Observations) lock(ctx context.Context, q *sqlc.Queries, project pgtype.UUID, session installation.MobileSession, r Observation) (lockedObservation, error) {
	var locked lockedObservation
	lineage, err := q.LockNotificationRuntime(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return locked, ErrExecutionHeld
	}
	if err != nil {
		return locked, err
	}
	if lineage.Sealed || lineage.MigrationHold {
		return locked, ErrExecutionHeld
	}
	app, err := q.LockExecutionApplication(ctx, project)
	if err != nil {
		return locked, err
	}
	actor := session.Principal()
	if app.Status != "ACTIVE" || app.ID != actor.ApplicationID {
		return locked, ErrObservationNotFound
	}
	_, err = q.LockInstallationUser(ctx, sqlc.LockInstallationUserParams{ApplicationID: app.ID, UserID: actor.UserID})
	if err != nil {
		return locked, err
	}
	if err = session.LockIdentity(ctx, q, project); err != nil {
		return locked, err
	}
	campaign, err := q.LockCampaignAcceptance(ctx, sqlc.LockCampaignAcceptanceParams{ApplicationID: app.ID, CampaignID: r.CampaignID})
	if err != nil {
		return locked, err
	}
	if campaign.Status != "PROCESSING" && campaign.Status != "COMPLETED" && campaign.Status != "FAILED" {
		return locked, ErrObservationNotFound
	}
	identity, err := q.GetExecutionIntentIdentity(ctx, sqlc.GetExecutionIntentIdentityParams{ApplicationID: app.ID, NotificationID: r.CampaignID, DeliveryID: r.DeliveryID})
	if err != nil {
		return locked, err
	}
	if identity.OwnerUserID != actor.UserID {
		return locked, ErrObservationNotFound
	}
	instance, err := q.LockExecutionInstallation(ctx, sqlc.LockExecutionInstallationParams{ApplicationID: app.ID, InstallationID: identity.InstallationID})
	if err != nil {
		return locked, err
	}
	if !instance.OwnerUserID.Valid || instance.OwnerUserID.Int64 != actor.UserID || instance.OwnerEpoch != identity.OwnerEpoch {
		return locked, ErrObservationNotFound
	}
	binding, err := q.LockExecutionBinding(ctx, sqlc.LockExecutionBindingParams{ApplicationID: app.ID, BindingID: identity.BindingID})
	if err != nil {
		return locked, err
	}
	if binding.InstallationID != instance.ID || binding.OwnerEpoch != identity.OwnerEpoch || binding.OwnerUserID != actor.UserID {
		return locked, ErrObservationNotFound
	}
	row, err := q.LockObservationDelivery(ctx, sqlc.LockObservationDeliveryParams{ApplicationID: app.ID, NotificationID: r.CampaignID, DeliveryID: r.DeliveryID})
	if err != nil {
		return locked, err
	}
	if row.AttemptCount < 1 {
		return locked, ErrObservationNotFound
	}
	m, err := q.GetFrozenReceiptMetadata(ctx, sqlc.GetFrozenReceiptMetadataParams{ApplicationID: app.ID, DeliveryID: r.DeliveryID})
	if err != nil {
		return locked, err
	}
	material := receipt.Material{Version: m.Version, KeyID: m.KeyID, Environment: m.Environment, Scope: receipt.Scope{
		ApplicationID: project, OriginEpoch: campaign.OriginEpoch, NotificationID: r.CampaignID, DeliveryID: r.DeliveryID,
		BindingID: identity.BindingID, InstallationID: instance.PublicID, OwnerEpoch: identity.OwnerEpoch, ExpiresUnix: m.ExpiresUnix}}
	if !m.CoversDeadline {
		return locked, ErrObservationNotFound
	}
	err = s.verifier.VerifyMaterial(r.Token, material, row.ReceivedAt.Time)
	if errors.Is(err, receipt.ErrInvalid) {
		return locked, ErrObservationNotFound
	}
	if err != nil {
		return locked, err
	}
	if !session.ValidAt(row.ReceivedAt.Time) {
		return locked, installation.ErrMobileCredential
	}
	if ok, delay := s.installationRate.Allow(instance.PublicID.String(), row.ReceivedAt.Time); !ok {
		return locked, &ObservationRateLimit{RetryAfter: delay}
	}
	if lineage.OriginEpoch != campaign.OriginEpoch || lineage.SourceEpoch != campaign.SourceEpoch {
		return locked, ErrExecutionHeld
	}
	if r.ObservedAt != nil && (r.ObservedAt.Before(row.CreatedAt.Time.Add(-5*time.Minute)) ||
		r.ObservedAt.After(row.ReceivedAt.Time.Add(5*time.Minute)) || r.ObservedAt.After(time.Unix(m.ExpiresUnix, 0).Add(5*time.Minute))) {
		return locked, ErrObservationInput
	}
	return lockedObservation{source: deliverySource{application: project, origin: campaign.OriginEpoch, source: campaign.SourceEpoch}, actor: actor, received: row.ReceivedAt, expires: m.ExpiresUnix}, nil
}

func (r Observation) apply(ctx context.Context, q *sqlc.Queries, locked lockedObservation) error {
	actor := locked.actor
	var observed pgtype.Timestamptz
	var normalized string
	if r.ObservedAt != nil {
		observed = resultTime(*r.ObservedAt)
		normalized = observed.Time.Format(time.RFC3339Nano)
	}
	// The verified capability includes the project, campaign, delivery, binding and epoch.
	digest := sha256.Sum256([]byte(r.Token + "\x00" + r.Event + "\x00" + normalized))
	added, err := q.RegisterReceiptRequest(ctx, sqlc.RegisterReceiptRequestParams{ApplicationID: actor.ApplicationID, UserID: actor.UserID,
		ClientEventID: r.ClientEventID, DeliveryID: r.DeliveryID, Fingerprint: digest[:], ExpiresUnix: locked.expires})
	if err != nil {
		return err
	}
	if added == 0 {
		existing, err := q.GetReceiptFingerprint(ctx, sqlc.GetReceiptFingerprintParams{ApplicationID: actor.ApplicationID, UserID: actor.UserID, ClientEventID: r.ClientEventID})
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, digest[:]) {
			return ErrObservationConflict
		}
		return nil
	}
	added, err = q.RecordObservationFact(ctx, sqlc.RecordObservationFactParams{ApplicationID: actor.ApplicationID, DeliveryID: r.DeliveryID, Event: r.Event, ReceivedAt: locked.received, ObservedAt: observed})
	if err != nil || added == 0 {
		return err
	}
	if r.Event == "OPENED" {
		added, err = q.RecordObservedRead(ctx, sqlc.RecordObservedReadParams{ApplicationID: pgtype.Int8{Int64: actor.ApplicationID, Valid: true},
			UserID: actor.UserID, NotificationID: r.CampaignID, ReceivedAt: locked.received})
		if err != nil {
			return err
		}
		if added == 0 {
			ok, err := q.CheckScopedObservedRead(ctx, sqlc.CheckScopedObservedReadParams{ApplicationID: actor.ApplicationID, UserID: actor.UserID, NotificationID: r.CampaignID})
			if err != nil {
				return err
			}
			if !ok {
				return ErrExecutionHeld
			}
		}
	}
	updated, err := q.ApplyDeliveryObservation(ctx, sqlc.ApplyDeliveryObservationParams{ApplicationID: actor.ApplicationID, DeliveryID: r.DeliveryID, Event: r.Event, ReceivedAt: locked.received})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	} // The fact added no new publishable timestamp.
	if err != nil {
		return err
	}
	return enqueueDeliveryUpdate(ctx, q, locked.source, "observation", updated, locked.received)
}

// ObservationRateLimit carries the seconds remaining in the installation's request window.
type ObservationRateLimit struct{ RetryAfter int }

func (*ObservationRateLimit) Error() string { return "receipt rate limit exceeded" }
