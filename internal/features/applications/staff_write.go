package applications

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

type staffWriteSession struct {
	id      pgtype.UUID
	actorID int64
	expires time.Time
}

func projectWriteSession(ctx context.Context) (staffWriteSession, error) {
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	var session pgtype.UUID
	if !ok || claims == nil || claims.StaffID <= 0 || claims.ExpiresAt == nil ||
		session.Scan(claims.SessionID) != nil || !session.Valid || session.Bytes == [16]byte{} {
		return staffWriteSession{}, authz.ErrUnauthenticated
	}
	return staffWriteSession{id: session, actorID: claims.StaffID, expires: claims.ExpiresAt.Time}, nil
}

// The caller holds session lineage and any required application lock first.
func (s staffWriteSession) lockActor(ctx context.Context, q *sqlc.Queries, epoch pgtype.UUID) error {
	if _, err := q.LockAcceptanceStaff(ctx, s.actorID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authz.ErrUnauthenticated
		}
		return err
	}
	return s.lockSession(ctx, q, epoch)
}

func (s staffWriteSession) lockSession(ctx context.Context, q *sqlc.Queries, epoch pgtype.UUID) error {
	_, err := q.LockStaffMutationSession(ctx, sqlc.LockStaffMutationSessionParams{
		Actor: s.actorID, SessionID: s.id, SessionEpoch: epoch,
	})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !time.Now().Before(s.expires)) {
		return authz.ErrUnauthenticated
	}
	return err
}

// Actor, session and lineage locks fence revocation. Expiry can still pass
// during later waits, so recheck it before committing the write and audit.
func (s staffWriteSession) checkActive(ctx context.Context, q *sqlc.Queries) error {
	active, err := q.StaffSessionActive(ctx, sqlc.StaffSessionActiveParams{SessionID: s.id, StaffID: s.actorID})
	if err != nil {
		return err
	}
	if !active || !time.Now().Before(s.expires) {
		return authz.ErrUnauthenticated
	}
	return nil
}

func (s *Service) writeAsStaff(ctx context.Context, work func(context.Context, *sqlc.Queries, staffWriteSession, pgtype.UUID) error) error {
	session, err := projectWriteSession(ctx)
	if err != nil {
		return err
	}
	return s.write(ctx, func(ctx context.Context, q *sqlc.Queries) error {
		epoch, err := q.LockStaffMutationLineage(ctx)
		if err != nil {
			return err
		}
		if err := work(ctx, q, session, epoch); err != nil {
			return err
		}
		return session.checkActive(ctx, q)
	})
}
