package dispatch

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
)

// ReplayDisposition is an advisory admission classification, not delivery status.
type ReplayDisposition string

const (
	ReplayEligible       ReplayDisposition = "eligible"
	ReplayRejected       ReplayDisposition = "rejected"
	ReplayPrepared       ReplayDisposition = "already_prepared"
	ReplayTerminal       ReplayDisposition = "terminal"
	ReplayExpired        ReplayDisposition = "expired"
	ReplayOnHold         ReplayDisposition = "held"
	ReplayReconciliation ReplayDisposition = "reconciliation_required"
	ReplayResolved       ReplayDisposition = "already_resolved"
)

// ReplayAssessment exposes no raw evidence. A notification identity is returned
// only after exact application and immutable acceptance matching.
type ReplayAssessment struct {
	Disposition    ReplayDisposition
	NotificationID int64
}

// Inspect performs an authorized dry run for one selected position. It writes no
// request, attempt, audit, claim, resolution or campaign state. Its result is
// advisory: execution must revalidate authority, acceptance and controls under
// its own locks before claiming or publishing.
func (s *ReplayRequests) Inspect(ctx context.Context, request ReplayRequest, credential string, position ReplayPosition) (ReplayAssessment, error) {
	var none ReplayAssessment
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return none, err
	}
	verified, err := s.verifier.Verify(request, credential)
	if err != nil {
		return none, err
	}
	if !slices.Contains(verified.request.Positions, position) {
		return none, ErrReplayInput
	}
	for _, selected := range verified.request.Positions {
		if selected.LogSourceID != s.sourceID {
			return none, ErrReplayDenied
		}
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return none, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New(tx)
	scope, err := s.lockAuthority(ctx, q, verified)
	if err != nil {
		return none, err
	}
	stored, err := q.LockReplayRequest(ctx, sqlc.LockReplayRequestParams{ApplicationID: scope.app.ID, ID: verified.request.ID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return none, err
	}
	if err == nil && !sameReplayHeader(stored, verified) {
		return none, ErrReplayConflict
	}
	result, err := inspectReplayPosition(ctx, q, scope, position)
	if err != nil {
		return none, err
	}
	if err := tx.Commit(ctx); err != nil {
		return none, err
	}
	return result, nil
}

func inspectReplayPosition(ctx context.Context, q *sqlc.Queries, scope replayScope, position ReplayPosition) (ReplayAssessment, error) {
	var none ReplayAssessment
	rejected := ReplayAssessment{Disposition: ReplayRejected}
	evidence, err := q.GetSendQuarantine(ctx, sqlc.GetSendQuarantineParams{LogSourceID: position.LogSourceID, OriginalPartition: position.Partition, OriginalOffset: position.Offset})
	if errors.Is(err, pgx.ErrNoRows) {
		return none, ErrReplayDenied
	}
	if err != nil {
		return none, err
	}
	event, err := kafka.DecodeScopedSend(evidence.OriginalKey, evidence.OriginalPayload)
	if err != nil {
		return rejected, nil
	}
	if event.ApplicationID != scope.app.PublicID.String() {
		return none, ErrReplayDenied
	}
	if event.OriginEpoch != scope.runtime.OriginEpoch.String() || event.SourceEpoch != scope.runtime.SourceEpoch.String() {
		return rejected, nil
	}
	policy, policyErr := q.GetApplicationDispatchPolicy(ctx, scope.app.ID)
	if policyErr != nil && !errors.Is(policyErr, pgx.ErrNoRows) {
		return none, policyErr
	}
	campaign, err := q.LockPreparationCampaign(ctx, sqlc.LockPreparationCampaignParams{ApplicationID: scope.app.ID, NotificationID: event.NotificationID})
	if errors.Is(err, pgx.ErrNoRows) {
		return rejected, nil
	}
	if err != nil {
		return none, err
	}
	if !acceptedSendMatches(campaign, event) {
		return rejected, nil
	}
	result := ReplayAssessment{NotificationID: campaign.ID}
	complete, markerErr := q.GetCompletePreparation(ctx, sqlc.GetCompletePreparationParams{ApplicationID: scope.app.ID, NotificationID: campaign.ID})
	if markerErr != nil && !errors.Is(markerErr, pgx.ErrNoRows) {
		return none, markerErr
	}
	if markerErr == nil && (complete.OriginEpoch != campaign.OriginEpoch || complete.SourceEpoch != campaign.SourceEpoch ||
		(campaign.Status != "PROCESSING" && campaign.Status != "COMPLETED" && campaign.Status != "FAILED")) {
		result.Disposition = ReplayReconciliation
		return result, nil
	}
	control, controlErr := q.LockReplayControl(ctx, sqlc.LockReplayControlParams{LogSourceID: position.LogSourceID, OriginalPartition: position.Partition, OriginalOffset: position.Offset})
	if controlErr != nil && !errors.Is(controlErr, pgx.ErrNoRows) {
		return none, controlErr
	}
	locked, err := q.LockReplayEvidence(ctx, sqlc.LockReplayEvidenceParams{LogSourceID: position.LogSourceID, OriginalPartition: position.Partition, OriginalOffset: position.Offset})
	if err != nil {
		return none, err
	}
	if !bytes.Equal(locked.OriginalKey, evidence.OriginalKey) || !bytes.Equal(locked.OriginalPayload, evidence.OriginalPayload) {
		return none, ErrQuarantineSource
	}
	switch {
	case controlErr == nil && control.ResolvedOutcome.Valid:
		if control.ResolvedApplicationID.Int64 != scope.app.ID {
			return none, ErrReplayDenied
		}
		result.Disposition = ReplayResolved
	case controlErr == nil && control.ActiveClaimToken.Valid:
		if control.ActiveApplicationID.Int64 != scope.app.ID {
			return none, ErrReplayDenied
		}
		result.Disposition = ReplayOnHold
	case markerErr == nil:
		result.Disposition = ReplayPrepared
	case campaign.Status == "COMPLETED" || campaign.Status == "FAILED":
		result.Disposition = ReplayTerminal
	case campaign.Status != "QUEUED":
		result.Disposition = ReplayReconciliation
	case campaign.Expired:
		result.Disposition = ReplayExpired
	case scope.app.Status != "ACTIVE" || scope.runtime.MigrationHold || policyErr != nil || policy.HoldNew || !policy.CoverageApproved:
		result.Disposition = ReplayOnHold
	default:
		due, err := q.ReplayPreparationRetryDue(ctx, sqlc.ReplayPreparationRetryDueParams{ApplicationID: scope.app.ID, NotificationID: campaign.ID})
		if err != nil {
			return none, err
		}
		if !due {
			result.Disposition = ReplayOnHold
			return result, nil
		}
		supported, err := q.ReplayTargetsSupported(ctx, sqlc.ReplayTargetsSupportedParams{ApplicationID: scope.app.ID, NotificationID: campaign.ID})
		if err != nil {
			return none, err
		}
		result.Disposition = ReplayEligible
		if !supported {
			result.Disposition = ReplayRejected
		}
	}
	return result, nil
}
