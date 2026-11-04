package dispatch

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/audit"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
	"hello/internal/middleware"
	"hello/internal/token"
)

var ErrReplayPublicationUnconfirmed = errors.New("replay publication requires reconciliation")

const (
	ReplayPublished          ReplayDisposition = "published"
	ReplayPublishUnconfirmed ReplayDisposition = "publish_unconfirmed"
	ReplayAbandoned          ReplayDisposition = "abandoned_unknown"
)

// ReplayExecutor publishes only newly committed claims after revalidation.
// It owns no goroutines or publisher shutdown. Cancellation can leave producer-
// owned I/O active; the caller must stop calls and drain the publisher before
// source handoff/restore. Neither a timeout nor a DB rollback undoes broker I/O.
type ReplayExecutor struct {
	requests  *ReplayRequests
	publisher Publisher
}

// NewReplayExecutor requires scoped request authority and a synchronous publisher.
// Production wiring must verify its broker/source manifest and own its drain.
func NewReplayExecutor(requests *ReplayRequests, publisher Publisher) (*ReplayExecutor, error) {
	if requests == nil || publisher == nil {
		return nil, ErrReplayInput
	}
	return &ReplayExecutor{requests: requests, publisher: publisher}, nil
}

// ReplayOne claims and publishes one position. It stops on uncertain prior work
// instead of recreating a lost execution proof or retrying external I/O.
func (e *ReplayExecutor) ReplayOne(ctx context.Context, request ReplayRequest, credential string, position ReplayPosition) (ReplayAttemptResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, err := e.requests.Claim(ctx, request, credential, position)
	if err != nil {
		return ReplayAttemptResult{}, err
	}
	if !result.Claimed {
		return result, replayOutcomeError(result.Disposition)
	}
	return e.Publish(ctx, request, credential, result)
}

func replayOutcomeError(outcome ReplayDisposition) error {
	switch outcome {
	case ReplayStarted, ReplayPublishUnconfirmed, ReplayAbandoned:
		return ErrReplayPublicationUnconfirmed
	default:
		return nil
	}
}

// Publish repeats current authority, acceptance, control and remaining-lease
// checks in a five-second transaction. It invokes the publisher at most once per
// in-memory proof, with a two-second call budget, and never retries its transaction.
// A successful result means broker acknowledgement, not delivery or preparation.
func (e *ReplayExecutor) Publish(ctx context.Context, request ReplayRequest, credential string, result ReplayAttemptResult) (ReplayAttemptResult, error) {
	var none ReplayAttemptResult
	proof := result.claim
	if proof == nil || !result.Claimed || !validReplayUUID(proof.token) {
		return none, ErrReplayInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return none, err
	}
	verified, err := e.requests.verifier.Verify(request, credential)
	if err != nil {
		return none, err
	}
	if verified.fingerprint != proof.provenance.fingerprint || verified.issuer != proof.provenance.issuer ||
		verified.deployment != proof.provenance.deployment || proof.position.LogSourceID != e.requests.sourceID ||
		!slices.Contains(verified.request.Positions, proof.position) {
		return none, ErrReplayDenied
	}
	tx, err := e.requests.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return none, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New(tx)
	scope, err := e.requests.lockAuthority(ctx, q, verified)
	if err != nil {
		return none, err
	}
	header, err := q.LockReplayRequest(ctx, sqlc.LockReplayRequestParams{ApplicationID: scope.app.ID, ID: verified.request.ID})
	if err != nil {
		return none, err
	}
	if !sameReplayHeader(header, verified) {
		return none, ErrReplayConflict
	}
	if err := requireReplaySelectionScope(ctx, q, verified.request); err != nil {
		return none, err
	}
	attempt, err := q.GetReplayAttempt(ctx, sqlc.GetReplayAttemptParams{
		ApplicationID: scope.app.ID, RequestID: verified.request.ID, LogSourceID: proof.position.LogSourceID,
		OriginalPartition: proof.position.Partition, OriginalOffset: proof.position.Offset,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return none, ErrReplayDenied
	}
	if err != nil {
		return none, err
	}
	if attempt.ClaimToken != proof.token {
		return none, ErrReplayDenied
	}
	if attempt.Outcome != string(ReplayStarted) {
		if err := tx.Commit(ctx); err != nil {
			return none, err
		}
		prior := ReplayAttemptResult{Disposition: ReplayDisposition(attempt.Outcome), NotificationID: attempt.NotificationID.Int64}
		return prior, replayOutcomeError(prior.Disposition)
	}
	assessment, err := assessReplayPosition(ctx, q, scope, proof.position, replayAssessmentOptions{
		ownerRequestID: verified.request.ID, ownerToken: proof.token,
	})
	if err != nil {
		return none, err
	}
	// Rejected/reconciliation assessments can return before the normal control
	// locks. Finalizing an existing claim still requires those locks in order.
	if _, err := q.LockReplayControl(ctx, sqlc.LockReplayControlParams{
		LogSourceID: proof.position.LogSourceID, OriginalPartition: proof.position.Partition, OriginalOffset: proof.position.Offset,
	}); err != nil {
		return none, err
	}
	if _, err := q.LockReplayEvidence(ctx, sqlc.LockReplayEvidenceParams{
		LogSourceID: proof.position.LogSourceID, OriginalPartition: proof.position.Partition, OriginalOffset: proof.position.Offset,
	}); err != nil {
		return none, err
	}
	outcome := assessment.Disposition
	var event kafka.ScopedSendRequested
	if outcome == ReplayEligible {
		evidence, err := q.GetSendQuarantine(ctx, sqlc.GetSendQuarantineParams{
			LogSourceID: proof.position.LogSourceID, OriginalPartition: proof.position.Partition, OriginalOffset: proof.position.Offset,
		})
		if err != nil {
			return none, err
		}
		event, err = kafka.DecodeScopedSend(evidence.OriginalKey, evidence.OriginalPayload)
		if err != nil {
			return none, err
		}
	}
	live, err := q.ReplayClaimPublishable(ctx, sqlc.ReplayClaimPublishableParams{
		ApplicationID: scope.app.ID, RequestID: verified.request.ID, LogSourceID: proof.position.LogSourceID,
		OriginalPartition: proof.position.Partition, OriginalOffset: proof.position.Offset, ClaimToken: proof.token,
		CredentialExpiresAt: pgtype.Timestamptz{Time: verified.expiresAt, Valid: true},
	})
	if err != nil {
		return none, err
	}
	if !live {
		return none, ErrReplayHeld
	}
	if outcome == ReplayEligible {
		// The pointer is shared by copied results. Once broker I/O starts, even a
		// failed result commit must not let that proof invoke the broker again.
		if !proof.publicationStarted.CompareAndSwap(false, true) {
			return none, ErrReplayPublicationUnconfirmed
		}
		brokerCtx, stop := context.WithTimeout(ctx, 2*time.Second)
		publishErr := safeReplayPublish(brokerCtx, e.publisher, event)
		if publishErr == nil {
			publishErr = brokerCtx.Err()
		}
		stop()
		outcome = ReplayPublished
		if publishErr != nil {
			outcome = ReplayPublishUnconfirmed
		}
	}
	if err := finishReplayResult(ctx, q, scope.app, verified, attempt, outcome); err != nil {
		return none, err
	}
	if err := tx.Commit(ctx); err != nil {
		return none, err
	}
	final := ReplayAttemptResult{Disposition: outcome, NotificationID: attempt.NotificationID.Int64}
	return final, replayOutcomeError(outcome)
}

func safeReplayPublish(ctx context.Context, publisher Publisher, event kafka.ScopedSendRequested) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrReplayPublicationUnconfirmed
		}
	}()
	return publisher.PublishScopedSend(ctx, event)
}

func finishReplayResult(ctx context.Context, q *sqlc.Queries, app sqlc.Application, verified VerifiedReplay, attempt sqlc.NotificationReplayAttempt, outcome ReplayDisposition) error {
	n, err := q.FinishReplayAttempt(ctx, sqlc.FinishReplayAttemptParams{
		ApplicationID: app.ID, RequestID: attempt.RequestID, LogSourceID: attempt.LogSourceID,
		OriginalPartition: attempt.OriginalPartition, OriginalOffset: attempt.OriginalOffset,
		ClaimToken: attempt.ClaimToken, Outcome: string(outcome),
	})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrReplayHeld
	}
	switch outcome {
	case ReplayPublished, ReplayPrepared, ReplayTerminal:
		n, err = q.ResolveReplayControl(ctx, sqlc.ResolveReplayControlParams{
			ApplicationID: pgtype.Int8{Int64: app.ID, Valid: true}, RequestID: attempt.RequestID, LogSourceID: attempt.LogSourceID,
			OriginalPartition: attempt.OriginalPartition, OriginalOffset: attempt.OriginalOffset,
			ExpectedToken: attempt.ClaimToken, Outcome: pgtype.Text{String: string(outcome), Valid: true},
		})
	default:
		n, err = q.ClearFinishedReplayClaim(ctx, sqlc.ClearFinishedReplayClaimParams{
			ApplicationID: app.ID, RequestID: attempt.RequestID, LogSourceID: attempt.LogSourceID,
			OriginalPartition: attempt.OriginalPartition, OriginalOffset: attempt.OriginalOffset, ClaimToken: attempt.ClaimToken,
		})
	}
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrReplayHeld
	}
	attempt.Outcome = string(outcome)
	auditCtx := context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: verified.staffID})
	return logReplayAttempt(auditCtx, q, app, attempt, audit.ACTION_NOTIFICATION_REPLAY_RESULT)
}
