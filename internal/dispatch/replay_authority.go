package dispatch

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/authz"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

type replayScope struct {
	app     sqlc.Application
	runtime sqlc.LockNotificationRuntimeRow
}

// lockAuthority must run in the caller-owned bounded transaction; these locks
// remain held through the subsequent replay mutation/result commit. Authority
// permits held-app bookkeeping; callers must separately gate new-work eligibility.
func (s *ReplayRequests) lockAuthority(ctx context.Context, q *sqlc.Queries, verified VerifiedReplay) (replayScope, error) {
	// Runtime -> source -> app -> staff -> operational grant. App locks serialize
	// explicit application-grant mutation; staff locks serialize role/status changes.
	runtime, err := q.LockNotificationRuntime(ctx)
	if err != nil {
		return replayScope{}, err
	}
	if runtime.Sealed {
		return replayScope{}, ErrReplayHeld
	}
	source, err := q.LockSendLogSource(ctx, s.sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return replayScope{}, ErrReplayDenied
	}
	if err != nil {
		return replayScope{}, err
	}
	if !source.Active {
		return replayScope{}, ErrReplayHeld
	}
	app, err := repository.NewApplicationRepository(q).LockByPublicID(ctx, verified.request.ApplicationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return replayScope{}, ErrReplayDenied
	}
	if err != nil {
		return replayScope{}, err
	}
	if _, err = q.LockAcceptanceStaff(ctx, verified.staffID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return replayScope{}, ErrReplayDenied
		}
		return replayScope{}, err
	}
	// This identity comes only from the pinned operational assertion, not a
	// caller-supplied staff ID or JWT role. Current role/grant still comes from DB.
	ctx = context.WithValue(ctx, middleware.UserContextKey, &token.AccessClaims{StaffID: verified.staffID})
	// Operational bookkeeping/inspection may classify no-ops during app holds.
	// Preserve current DB membership and send-role authority; eligibility, not
	// this metadata guard, decides whether ACTIVE work may actually be replayed.
	authority, err := authz.NewApplicationAuthorizer(q).RequireApplication(ctx, verified.request.ApplicationID, authz.ApplicationsRead)
	if err != nil {
		if errors.Is(err, authz.ErrUnauthenticated) || errors.Is(err, authz.ErrForbidden) ||
			errors.Is(err, authz.ErrApplicationNotFound) || errors.Is(err, authz.ErrApplicationNotActive) {
			return replayScope{}, ErrReplayDenied
		}
		return replayScope{}, err
	}
	if !authz.Allowed(authority.Staff.Role, authz.NotificationsSend) {
		return replayScope{}, ErrReplayDenied
	}
	grant, err := q.LockReplayOperationalGrant(ctx, sqlc.LockReplayOperationalGrantParams{
		ID: verified.grantID, ApplicationID: app.ID, StaffUserID: verified.staffID, RequestID: verified.request.ID,
		Issuer: verified.issuer, Deployment: verified.deployment, SelectionHash: verified.fingerprint[:],
		CredentialExpiresAt: pgtype.Timestamptz{Time: verified.expiresAt, Valid: true},
		AuthenticatedAt:     pgtype.Timestamptz{Time: verified.authenticatedAt, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return replayScope{}, ErrReplayDenied
	}
	if err != nil {
		return replayScope{}, err
	}
	if !grant.Usable {
		return replayScope{}, ErrReplayDenied
	}
	return replayScope{app: app, runtime: runtime}, nil
}
