package dispatch

import (
	"context"
	"crypto/rand"
	"errors"
	"slices"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/audit"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

// ReplayStarted records a durable unexecuted attempt, not broker acceptance.
const ReplayStarted ReplayDisposition = "started"

// ReplayAttemptResult reports durable admission evidence, not provider delivery.
// Claimed is true only for a new claim whose commit was acknowledged in this call.
// Do not log the whole value: its private execution proof contains a claim token.
type ReplayAttemptResult struct {
	Disposition    ReplayDisposition
	NotificationID int64
	Claimed        bool
	claim          *replayClaim
}

type replayClaim struct {
	publicationStarted atomic.Bool
	position           ReplayPosition
	token              pgtype.UUID
	provenance         VerifiedReplay
}

func newReplayClaimToken() (pgtype.UUID, error) {
	var id pgtype.UUID
	if _, err := rand.Read(id.Bytes[:]); err != nil {
		return id, err
	}
	id.Bytes[6] = (id.Bytes[6] & 0x0f) | 0x40
	id.Bytes[8] = (id.Bytes[8] & 0x3f) | 0x80
	id.Valid = true
	return id, nil
}

// Claim registers the full request, then atomically records one position's
// classification, optional exclusive claim and required audit. Each transaction
// is bounded to five seconds. It never calls a broker or resets campaign state.
// A retry returns existing evidence without reissuing a lost execution proof;
// unknown commit outcomes cannot authorize publication by this invocation.
func (s *ReplayRequests) Claim(ctx context.Context, request ReplayRequest, credential string, position ReplayPosition) (ReplayAttemptResult, error) {
	var none ReplayAttemptResult
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	verified, err := s.verifier.Verify(request, credential)
	if err != nil {
		return none, err
	}
	if !slices.Contains(verified.request.Positions, position) {
		return none, ErrReplayInput
	}
	if _, err := s.Register(ctx, request, credential); err != nil {
		return none, err
	}
	claimCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	tx, err := s.db.BeginTx(claimCtx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return none, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New(tx)
	scope, err := s.lockAuthority(claimCtx, q, verified)
	if err != nil {
		return none, err
	}
	stored, err := q.LockReplayRequest(claimCtx, sqlc.LockReplayRequestParams{ApplicationID: scope.app.ID, ID: verified.request.ID})
	if err != nil {
		return none, err
	}
	if !sameReplayHeader(stored, verified) {
		return none, ErrReplayConflict
	}
	previous, err := q.GetReplayAttempt(claimCtx, sqlc.GetReplayAttemptParams{
		ApplicationID: scope.app.ID, RequestID: verified.request.ID, LogSourceID: position.LogSourceID,
		OriginalPartition: position.Partition, OriginalOffset: position.Offset,
	})
	if err == nil {
		if err := tx.Commit(claimCtx); err != nil {
			return none, err
		}
		return ReplayAttemptResult{Disposition: ReplayDisposition(previous.Outcome), NotificationID: previous.NotificationID.Int64}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return none, err
	}
	assessment, err := assessReplayPosition(claimCtx, q, scope, position, replayAssessmentOptions{reserveControl: true})
	if err != nil {
		return none, err
	}
	outcome := string(assessment.Disposition)
	var claimToken pgtype.UUID
	if assessment.Disposition == ReplayEligible {
		outcome = string(ReplayStarted)
		claimToken, err = newReplayClaimToken()
		if err != nil {
			return none, err
		}
	}
	params := sqlc.InsertReplayAttemptParams{
		ApplicationID: scope.app.ID, RequestID: verified.request.ID, LogSourceID: position.LogSourceID,
		OriginalPartition: position.Partition, OriginalOffset: position.Offset, Outcome: outcome, ClaimToken: claimToken,
	}
	if assessment.NotificationID > 0 {
		params.NotificationID = pgtype.Int8{Int64: assessment.NotificationID, Valid: true}
		params.OriginEpoch, params.SourceEpoch = scope.runtime.OriginEpoch, scope.runtime.SourceEpoch
	}
	attempt, err := q.InsertReplayAttempt(claimCtx, params)
	if err != nil {
		return none, err
	}
	if claimToken.Valid {
		n, err := q.ClaimReplayControl(claimCtx, sqlc.ClaimReplayControlParams{
			ApplicationID: pgtype.Int8{Int64: scope.app.ID, Valid: true}, RequestID: verified.request.ID, ClaimToken: claimToken,
			LogSourceID: position.LogSourceID, OriginalPartition: position.Partition, OriginalOffset: position.Offset,
		})
		if err != nil {
			return none, err
		}
		if n != 1 {
			return none, ErrReplayHeld
		}
	} else if assessment.Disposition == ReplayPrepared || assessment.Disposition == ReplayTerminal {
		n, err := q.ResolveReplayControl(claimCtx, sqlc.ResolveReplayControlParams{
			ApplicationID: pgtype.Int8{Int64: scope.app.ID, Valid: true}, RequestID: verified.request.ID,
			LogSourceID: position.LogSourceID, OriginalPartition: position.Partition, OriginalOffset: position.Offset,
			Outcome: pgtype.Text{String: outcome, Valid: true},
		})
		if err != nil {
			return none, err
		}
		if n != 1 {
			return none, ErrReplayHeld
		}
	}
	auditCtx := context.WithValue(claimCtx, middleware.UserContextKey, &token.AccessClaims{StaffID: verified.staffID})
	if err := logReplayAttempt(auditCtx, q, scope.app, attempt, audit.ACTION_NOTIFICATION_REPLAY_ATTEMPT); err != nil {
		return none, err
	}
	if err := tx.Commit(claimCtx); err != nil {
		return none, err
	}
	result := ReplayAttemptResult{Disposition: ReplayDisposition(outcome), NotificationID: assessment.NotificationID, Claimed: claimToken.Valid}
	if claimToken.Valid {
		result.claim = &replayClaim{position: position, token: claimToken, provenance: verified}
	}
	return result, nil
}

func logReplayAttempt(ctx context.Context, q *sqlc.Queries, app sqlc.Application, attempt sqlc.NotificationReplayAttempt, action string) error {
	metadata := struct {
		RequestID string `json:"request_id"`
		SourceID  string `json:"log_source_id"`
		Partition int32  `json:"partition"`
		Offset    int64  `json:"offset"`
		Outcome   string `json:"outcome"`
		Executor  string `json:"executor,omitempty"`
	}{RequestID: attempt.RequestID.String(), SourceID: attempt.LogSourceID.String(), Partition: attempt.OriginalPartition, Offset: attempt.OriginalOffset, Outcome: attempt.Outcome}
	if action == audit.ACTION_NOTIFICATION_REPLAY_ABANDONED {
		metadata.Executor = "expired_claim_recovery"
	}
	return audit.NewAuditService(repository.NewAuditLogRepository(q)).Log(ctx, audit.LogParams{
		ApplicationID: app.ID, Action: action, EntityType: audit.ENTITY_TYPE_APPLICATION, EntityID: app.ID, EntityName: app.Name, After: metadata,
	})
}
