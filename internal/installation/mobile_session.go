package installation

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

// MobileSession binds an authenticated bearer to its verified project, subject
// mapping and expiry. Only the mobile authority constructs a valid session.
type MobileSession struct {
	actor              Principal
	project            pgtype.UUID
	namespace, subject string
	expires            time.Time
	enrollmentVerified bool
}

// Principal returns the identity resolved at bearer authentication.
func (s MobileSession) Principal() Principal { return s.actor }

// AuthenticateSession verifies the configured bearer and resolves its current project user.
func (a *MobileAuthority) AuthenticateSession(ctx context.Context, project pgtype.UUID, credential string) (MobileSession, error) {
	return a.authenticate(ctx, project, credential, nil)
}

// AuthenticateEnrollmentSession also verifies origin evidence for this bearer.
// A consumed origin assertion may require replacement if enrollment later fails.
func (a *MobileAuthority) AuthenticateEnrollmentSession(ctx context.Context, project pgtype.UUID, credential, evidence string) (MobileSession, error) {
	return a.authenticate(ctx, project, credential, &evidence)
}

// LockIdentity rechecks the subject mapping and expiry against database time.
// Call within the mutation transaction after locking the application and active user.
func (s MobileSession) LockIdentity(ctx context.Context, q *sqlc.Queries, project pgtype.UUID) error {
	if q == nil || !project.Valid || s.project != project || s.actor.UserID <= 0 {
		return ErrMobileCredential
	}
	_, err := q.LockReceiptIdentity(ctx, sqlc.LockReceiptIdentityParams{ApplicationID: s.actor.ApplicationID, UserID: s.actor.UserID,
		IdentityNamespace: s.namespace, Subject: s.subject, SessionExpiresAt: pgtype.Timestamptz{Time: s.expires, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMobileCredential
	}
	return err
}

// ValidAt checks session expiry after potentially blocking ledger locks.
func (s MobileSession) ValidAt(at time.Time) bool { return at.Before(s.expires) }
