// Package staffmutation binds a v1 staff write to its authenticated session,
// current actor, target and database transaction.
package staffmutation

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"
)

// Transactor starts one independent transaction per mutation.
type Transactor interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// Run locks lineage, users in ID order and the actor's session before work.
// Administrative calls also serialize changes to the active super-admin roster.
func Run[T any](ctx context.Context, db Transactor, target int64, administrative bool, work func(*sqlc.Queries) (T, error)) (T, error) {
	var zero, result T
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	var session pgtype.UUID
	if !ok || claims == nil || claims.StaffID <= 0 || session.Scan(claims.SessionID) != nil || !session.Valid {
		return zero, authz.ErrUnauthenticated
	}
	if !administrative && claims.StaffID != target {
		return zero, authz.ErrForbidden
	}
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if administrative {
			if _, err := q.LockStaffAdministration(ctx); err != nil {
				return err
			}
		}
		epoch, err := q.LockStaffMutationLineage(ctx)
		if err != nil {
			return err
		}
		if _, err = q.LockStaffMutationUsers(ctx, sqlc.LockStaffMutationUsersParams{Actor: claims.StaffID, Target: target}); err != nil {
			return err
		}
		_, err = q.LockStaffMutationSession(ctx, sqlc.LockStaffMutationSessionParams{Actor: claims.StaffID, SessionID: session, SessionEpoch: epoch})
		if errors.Is(err, pgx.ErrNoRows) {
			return authz.ErrUnauthenticated
		}
		if err != nil {
			return err
		}
		result, err = work(q)
		return err
	})
	if err != nil {
		return zero, err
	}
	return result, nil
}

// EmailConflict identifies only the staff email uniqueness constraint.
func EmailConflict(err error) bool {
	var p *pgconn.PgError
	return errors.As(err, &p) && p.Code == "23505" && p.TableName == "staff_users" && p.ConstraintName == "staff_users_email_key"
}
