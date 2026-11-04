package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
)

var (
	ErrAdmissionEnvelope = errors.New("send envelope does not match accepted campaign")
	ErrAdmissionHeld     = errors.New("campaign preparation is held")
	ErrAdmissionState    = errors.New("campaign preparation requires reconciliation")
	ErrAdmissionTarget   = errors.New("unsupported campaign targets")
)

// PreparationResult distinguishes unknown terminal audience from a known empty
// snapshot. Counts never come from live user queries on replay.
type PreparationResult struct {
	SnapshotComplete bool
	ResolvedUsers    int64
	EligibleUsers    int64
	EligibleDevices  int64
}

// Preparer owns atomic audience snapshots and intent creation for accepted sends.
type Preparer struct{ db Transactor }

// NewPreparer requires a database; preparation performs no broker or provider I/O.
func NewPreparer(db Transactor) (*Preparer, error) {
	if db == nil {
		return nil, errors.New("preparation requires database")
	}
	return &Preparer{db: db}, nil
}

func preparationResult(row sqlc.NotificationPreparation) PreparationResult {
	return PreparationResult{
		SnapshotComplete: true,
		ResolvedUsers:    row.ResolvedUserCount,
		EligibleUsers:    row.EligibleUserCount,
		EligibleDevices:  row.EligibleDeviceCount,
	}
}

// Prepare atomically persists the complete audience and intents without network
// I/O. A committed hold is still an error: it must never advance a Kafka offset
// as if a complete snapshot had been prepared.
func (p *Preparer) Prepare(ctx context.Context, event kafka.ScopedSendRequested) (PreparationResult, error) {
	if err := event.Validate(); err != nil {
		return PreparationResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		result, err := p.prepareOnce(ctx, event)
		if err == nil {
			return result, nil
		}
		var dbErr *pgconn.PgError
		if !errors.As(err, &dbErr) || (dbErr.Code != "40001" && dbErr.Code != "40P01") || attempt == 2 {
			return PreparationResult{}, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return PreparationResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	return PreparationResult{}, errors.New("preparation retries exhausted")
}

func (p *Preparer) prepareOnce(ctx context.Context, event kafka.ScopedSendRequested) (PreparationResult, error) {
	tx, err := p.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return PreparationResult{}, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New(tx)
	runtime, err := q.LockNotificationRuntime(ctx)
	if err != nil {
		return PreparationResult{}, err
	}
	if runtime.Sealed {
		return PreparationResult{}, ErrAdmissionHeld
	}
	if runtime.OriginEpoch.String() != event.OriginEpoch || runtime.SourceEpoch.String() != event.SourceEpoch {
		return PreparationResult{}, ErrAdmissionEnvelope
	}
	var project pgtype.UUID
	_ = project.Scan(event.ApplicationID) // Validate already required a canonical UUID.
	app, err := repository.NewApplicationRepository(q).LockByPublicID(ctx, project)
	if errors.Is(err, pgx.ErrNoRows) {
		return PreparationResult{}, ErrAdmissionEnvelope
	}
	if err != nil {
		return PreparationResult{}, err
	}
	policy, policyErr := q.GetApplicationDispatchPolicy(ctx, app.ID)
	if policyErr != nil && !errors.Is(policyErr, pgx.ErrNoRows) {
		return PreparationResult{}, policyErr
	}
	campaign, err := q.LockPreparationCampaign(ctx, sqlc.LockPreparationCampaignParams{ApplicationID: app.ID, NotificationID: event.NotificationID})
	if errors.Is(err, pgx.ErrNoRows) {
		return PreparationResult{}, ErrAdmissionEnvelope
	}
	if err != nil {
		return PreparationResult{}, err
	}
	if !campaign.OriginEpoch.Valid || !campaign.SourceEpoch.Valid || !campaign.QueuedAt.Valid || !campaign.DeadlineAt.Valid ||
		campaign.OriginEpoch.String() != event.OriginEpoch || campaign.SourceEpoch.String() != event.SourceEpoch ||
		!campaign.QueuedAt.Time.Equal(event.RequestedAt) || !campaign.DeadlineAt.Time.Equal(event.DeadlineAt) ||
		campaign.ScheduledAt.Valid != (event.ScheduledFor != nil) ||
		(event.ScheduledFor != nil && !campaign.ScheduledAt.Time.Equal(*event.ScheduledFor)) {
		return PreparationResult{}, ErrAdmissionEnvelope
	}
	complete, err := q.GetCompletePreparation(ctx, sqlc.GetCompletePreparationParams{ApplicationID: app.ID, NotificationID: campaign.ID})
	if err == nil {
		if complete.OriginEpoch != campaign.OriginEpoch || complete.SourceEpoch != campaign.SourceEpoch ||
			(campaign.Status != "PROCESSING" && campaign.Status != "COMPLETED" && campaign.Status != "FAILED") {
			return PreparationResult{}, ErrAdmissionState
		}
		return preparationResult(complete), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PreparationResult{}, err
	}
	if campaign.Status == "COMPLETED" || campaign.Status == "FAILED" {
		return PreparationResult{}, nil
	}
	if campaign.Status != "QUEUED" {
		return PreparationResult{}, ErrAdmissionState
	}
	if campaign.Expired {
		if err := q.ExpireUnpreparedCampaign(ctx, sqlc.ExpireUnpreparedCampaignParams{ApplicationID: app.ID, NotificationID: campaign.ID}); err != nil {
			return PreparationResult{}, err
		}
		return PreparationResult{}, tx.Commit(ctx)
	}
	hold := func(reason string, cause error) (PreparationResult, error) {
		if err := q.HoldCampaignPreparation(ctx, sqlc.HoldCampaignPreparationParams{ApplicationID: app.ID, NotificationID: campaign.ID, Reason: pgtype.Text{String: reason, Valid: true}}); err != nil {
			return PreparationResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return PreparationResult{}, err
		}
		return PreparationResult{}, cause
	}
	if runtime.MigrationHold {
		return hold("migration_hold", ErrAdmissionHeld)
	}
	if app.Status != "ACTIVE" || policyErr != nil || policy.HoldNew {
		return hold("project_hold", ErrAdmissionHeld)
	}
	if !policy.CoverageApproved {
		return hold("audience_not_ready", ErrAdmissionHeld)
	}
	counts, err := q.InsertPreparationIntents(ctx, sqlc.InsertPreparationIntentsParams{ApplicationID: app.ID, NotificationID: campaign.ID})
	if err != nil {
		return PreparationResult{}, err
	}
	if !counts.Supported {
		return hold("unsupported_targets", ErrAdmissionTarget)
	}
	if !counts.Covered {
		return hold("audience_not_ready", ErrAdmissionHeld)
	}
	complete, err = q.CompleteCampaignPreparation(ctx, sqlc.CompleteCampaignPreparationParams{ApplicationID: app.ID, NotificationID: campaign.ID,
		OriginEpoch: campaign.OriginEpoch, SourceEpoch: campaign.SourceEpoch, ResolvedUsers: counts.ResolvedUsers,
		EligibleUsers: counts.EligibleUsers, EligibleDevices: counts.EligibleDevices})
	if err != nil {
		return PreparationResult{}, err
	}
	if err := q.MarkCampaignPrepared(ctx, sqlc.MarkCampaignPreparedParams{ApplicationID: app.ID, NotificationID: campaign.ID, EmptyAudience: counts.EligibleDevices == 0}); err != nil {
		return PreparationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PreparationResult{}, err
	}
	return preparationResult(complete), nil
}
