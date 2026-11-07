package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"hello/internal/audit"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

// ReplayRecoveryReport retains acknowledged cleanup progress on partial failure.
type ReplayRecoveryReport struct {
	Visited   int
	Abandoned int
	Skipped   int
}

// ReplayRecovery releases only database-expired claims as publication-uncertain.
// It never publishes, resolves evidence or requires the original actor to remain
// active. This service-owned maintenance path has no public route or issuer role.
type ReplayRecovery struct{ db Transactor }

// NewReplayRecovery requires the same database used by replay request execution.
func NewReplayRecovery(db Transactor) (*ReplayRecovery, error) {
	if db == nil {
		return nil, ErrReplayInput
	}
	return &ReplayRecovery{db: db}, nil
}

// Tick discovers at most 100 expired candidates without child locks, then
// reacquires canonical locks and rechecks each lease in its own transaction.
// The caller owns polling/backoff; the service owns no goroutines.
func (r *ReplayRecovery) Tick(ctx context.Context, pageSize int32) (ReplayRecoveryReport, error) {
	var report ReplayRecoveryReport
	if pageSize < 1 || pageSize > 100 {
		return report, ErrReplayInput
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := r.expiredClaims(ctx, pageSize)
	if err != nil {
		return report, err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.Visited++
		changed, err := r.recoverOne(ctx, row)
		if err != nil {
			return report, err
		}
		if changed {
			report.Abandoned++
		} else {
			report.Skipped++
		}
	}
	return report, nil
}

func (r *ReplayRecovery) expiredClaims(ctx context.Context, pageSize int32) ([]sqlc.ListExpiredReplayClaimsRow, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	rows, err := sqlc.New(tx).ListExpiredReplayClaims(ctx, pageSize)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *ReplayRecovery) recoverOne(ctx context.Context, candidate sqlc.ListExpiredReplayClaimsRow) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New(tx)
	// Cleanup locks controls even while sealed/inactive. It grants no authority
	// to admit work and must not strand expired claims after operator offboarding.
	if _, err := q.LockNotificationRuntime(ctx); err != nil {
		return false, err
	}
	if _, err := q.LockSendLogSource(ctx, candidate.LogSourceID); err != nil {
		return false, err
	}
	metadata, err := q.ReplayCleanupContext(ctx, sqlc.ReplayCleanupContextParams{
		ApplicationID: candidate.ApplicationID, RequestID: candidate.RequestID, LogSourceID: candidate.LogSourceID,
		OriginalPartition: candidate.OriginalPartition, OriginalOffset: candidate.OriginalOffset,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	app, err := repository.NewApplicationRepository(q).LockByPublicID(ctx, metadata.ApplicationPublicID)
	if err != nil {
		return false, err
	}
	if _, err := q.LockAcceptanceStaff(ctx, metadata.StaffUserID); err != nil {
		return false, err
	}
	_, err = q.LockReplayCleanupGrant(ctx, sqlc.LockReplayCleanupGrantParams{
		ID: metadata.OperationalGrantID, ApplicationID: app.ID, StaffUserID: metadata.StaffUserID,
	})
	// Older retained provenance can lack a grant row. Cleanup neither validates
	// that grant nor turns it into executable authority.
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if _, err := q.LockReplayRequest(ctx, sqlc.LockReplayRequestParams{ApplicationID: app.ID, ID: candidate.RequestID}); err != nil {
		return false, err
	}
	if _, err := q.GetApplicationDispatchPolicy(ctx, app.ID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if !metadata.NotificationID.Valid {
		return false, ErrReplayInput
	}
	if _, err := q.LockPreparationCampaign(ctx, sqlc.LockPreparationCampaignParams{ApplicationID: app.ID, NotificationID: metadata.NotificationID.Int64}); err != nil {
		return false, err
	}
	control, err := q.LockReplayControl(ctx, sqlc.LockReplayControlParams{
		LogSourceID: candidate.LogSourceID, OriginalPartition: candidate.OriginalPartition, OriginalOffset: candidate.OriginalOffset,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if control.ActiveApplicationID.Int64 != app.ID || control.ActiveRequestID != candidate.RequestID || control.ActiveClaimToken != candidate.ClaimToken {
		return false, nil
	}
	if _, err := q.LockReplayCleanupEvidence(ctx, sqlc.LockReplayCleanupEvidenceParams{
		LogSourceID: candidate.LogSourceID, OriginalPartition: candidate.OriginalPartition, OriginalOffset: candidate.OriginalOffset,
	}); err != nil {
		return false, err
	}
	n, err := q.AbandonExpiredReplayAttempt(ctx, sqlc.AbandonExpiredReplayAttemptParams{
		ApplicationID: app.ID, RequestID: candidate.RequestID, LogSourceID: candidate.LogSourceID,
		OriginalPartition: candidate.OriginalPartition, OriginalOffset: candidate.OriginalOffset, ClaimToken: candidate.ClaimToken,
	})
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	n, err = q.ClearFinishedReplayClaim(ctx, sqlc.ClearFinishedReplayClaimParams{
		ApplicationID: app.ID, RequestID: candidate.RequestID, LogSourceID: candidate.LogSourceID,
		OriginalPartition: candidate.OriginalPartition, OriginalOffset: candidate.OriginalOffset, ClaimToken: candidate.ClaimToken,
	})
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, ErrReplayHeld
	}
	attempt, err := q.GetReplayAttempt(ctx, sqlc.GetReplayAttemptParams{
		ApplicationID: app.ID, RequestID: candidate.RequestID, LogSourceID: candidate.LogSourceID,
		OriginalPartition: candidate.OriginalPartition, OriginalOffset: candidate.OriginalOffset,
	})
	if err != nil {
		return false, err
	}
	auditCtx := context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: metadata.StaffUserID})
	if err := logReplayAttempt(auditCtx, q, app, attempt, audit.ACTION_NOTIFICATION_REPLAY_ABANDONED); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
