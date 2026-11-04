package dispatch

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

var (
	ErrReplayDenied   = errors.New("operational replay authority required")
	ErrReplayHeld     = errors.New("operational replay is held")
	ErrReplayConflict = errors.New("replay request identity conflicts")
)

// ReplayRegistration reports a committed request header, never a publication.
type ReplayRegistration struct {
	RequestID         pgtype.UUID
	AlreadyRegistered bool
}

// ReplayRequests registers authorized, immutable replay selections with atomic
// audit. It creates no claims, resolves no evidence and publishes no messages.
type ReplayRequests struct {
	db       Transactor
	verifier *ReplayVerifier
	sourceID pgtype.UUID
}

// NewReplayRequests pins the configured current send source and credential verifier.
// Source manifest/broker verification is still required before publication wiring.
func NewReplayRequests(db Transactor, verifier *ReplayVerifier, sourceID pgtype.UUID) (*ReplayRequests, error) {
	if db == nil || verifier == nil || len(verifier.key) != ed25519.PublicKeySize || !validReplayUUID(sourceID) {
		return nil, ErrReplayInput
	}
	return &ReplayRequests{db: db, verifier: verifier, sourceID: sourceID}, nil
}

// Register verifies scoped step-up evidence, locks current authority and records
// a request plus audit in one bounded transaction. Identical retries reauthorize
// but add no audit; changed identity/fingerprint is a conflict. Commit errors
// return no success and require reconciliation using the same request UUID.
func (s *ReplayRequests) Register(ctx context.Context, request ReplayRequest, credential string) (ReplayRegistration, error) {
	var none ReplayRegistration
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return none, err
	}
	verified, err := s.verifier.Verify(request, credential)
	if err != nil {
		return none, err
	}
	for _, position := range verified.request.Positions {
		if position.LogSourceID != s.sourceID {
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
	// Runtime -> source -> app -> staff -> operational grant. App locks serialize
	// explicit application-grant mutation; staff locks serialize role/status changes.
	runtime, err := q.LockNotificationRuntime(ctx)
	if err != nil {
		return none, err
	}
	if runtime.Sealed {
		return none, ErrReplayHeld
	}
	source, err := q.LockSendLogSource(ctx, s.sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return none, ErrReplayDenied
	}
	if err != nil {
		return none, err
	}
	if !source.Active {
		return none, ErrReplayHeld
	}
	app, err := repository.NewApplicationRepository(q).LockByPublicID(ctx, verified.request.ApplicationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return none, ErrReplayDenied
	}
	if err != nil {
		return none, err
	}
	if _, err = q.LockAcceptanceStaff(ctx, verified.staffID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return none, ErrReplayDenied
		}
		return none, err
	}
	// This identity comes only from the pinned operational assertion, not a
	// caller-supplied staff ID or JWT role. Current role/grant still comes from DB.
	ctx = context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: verified.staffID})
	if _, err = authz.NewApplicationAuthorizer(q).RequireApplication(ctx, verified.request.ApplicationID, authz.NotificationsSend); err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) || errors.Is(err, authz.ErrForbidden) ||
			errors.Is(err, authz.ErrApplicationNotFound) || errors.Is(err, authz.ErrApplicationNotActive) {
			return none, ErrReplayDenied
		}
		return none, err
	}
	grant, err := q.LockReplayOperationalGrant(ctx, sqlc.LockReplayOperationalGrantParams{
		ID: verified.grantID, ApplicationID: app.ID, StaffUserID: verified.staffID, RequestID: verified.request.ID,
		Issuer: verified.issuer, Deployment: verified.deployment, SelectionHash: verified.fingerprint[:],
		CredentialExpiresAt: pgtype.Timestamptz{Time: verified.expiresAt, Valid: true},
		AuthenticatedAt:     pgtype.Timestamptz{Time: verified.authenticatedAt, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return none, ErrReplayDenied
	}
	if err != nil {
		return none, err
	}
	if !grant.Usable {
		return none, ErrReplayDenied
	}
	n, err := q.InsertReplayRequest(ctx, sqlc.InsertReplayRequestParams{
		ApplicationID: app.ID, ID: verified.request.ID, StaffUserID: verified.staffID, OperationalGrantID: verified.grantID,
		SelectionHash: verified.fingerprint[:], ChangeReference: verified.request.ChangeReference,
	})
	if err != nil {
		return none, err
	}
	stored, err := q.LockReplayRequest(ctx, sqlc.LockReplayRequestParams{ApplicationID: app.ID, ID: verified.request.ID})
	if err != nil {
		return none, err
	}
	if stored.StaffUserID != verified.staffID || stored.OperationalGrantID != verified.grantID ||
		!bytes.Equal(stored.SelectionHash, verified.fingerprint[:]) || stored.ChangeReference != verified.request.ChangeReference {
		return none, ErrReplayConflict
	}
	if n == 1 {
		evidence := struct {
			RequestID       string `json:"request_id"`
			GrantID         string `json:"operational_grant_id"`
			SourceID        string `json:"log_source_id"`
			PositionCount   int    `json:"position_count"`
			ChangeReference string `json:"change_reference"`
		}{RequestID: stored.ID.String(), GrantID: stored.OperationalGrantID.String(), SourceID: s.sourceID.String(), PositionCount: len(verified.request.Positions), ChangeReference: stored.ChangeReference}
		err = audit.NewAuditService(repository.NewAuditLogRepository(q)).Log(ctx, audit.LogParams{
			ApplicationID: app.ID, Action: audit.ACTION_NOTIFICATION_REPLAY_REQUEST, EntityType: audit.ENTITY_TYPE_APPLICATION,
			EntityID: app.ID, EntityName: app.Name, After: evidence,
		})
		if err != nil {
			return none, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return none, err
	}
	return ReplayRegistration{RequestID: stored.ID, AlreadyRegistered: n == 0}, nil
}
