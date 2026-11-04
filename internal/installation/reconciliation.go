package installation

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

type StateSnapshot struct {
	State
	HasActiveBinding bool
}

// ReadState permits reconciliation after logout using retained proof. A current
// owner belonging to another account remains hidden from the authenticated user.
func (s *Lifecycle) ReadState(ctx context.Context, actor Principal, id pgtype.UUID, proof string) (StateSnapshot, error) {
	var result StateSnapshot
	err := s.write(ctx, func(q *sqlc.Queries) error {
		if err := requireLifecycleActor(ctx, q, actor); err != nil {
			return err
		}
		row, err := q.LockMobileInstallation(ctx, sqlc.LockMobileInstallationParams{ApplicationID: actor.ApplicationID, PublicID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !VerifyProof(proof, row.ProofVerifier) || (row.OwnerUserID.Valid && row.OwnerUserID.Int64 != actor.UserID) {
			return ErrNotFound
		}
		_, err = q.LockActiveInstallationBinding(ctx, sqlc.LockActiveInstallationBindingParams{ApplicationID: actor.ApplicationID, InstallationID: row.ID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		result = StateSnapshot{State: installationState(row), HasActiveBinding: err == nil}
		return nil
	})
	if err != nil {
		return StateSnapshot{}, err
	}
	return result, nil
}

// Invalidate retires only the delivery's original current generation. It is an
// internal worker operation, including when project dispatch has been held.
func (s *Lifecycle) Invalidate(ctx context.Context, applicationID int64, id pgtype.UUID, bindingID, ownerEpoch int64) (bool, error) {
	if applicationID <= 0 || !id.Valid || bindingID <= 0 || ownerEpoch <= 0 {
		return false, ErrTransition
	}
	changed := false
	err := s.write(ctx, func(q *sqlc.Queries) error {
		changed = false
		if _, err := q.LockInstallationProject(ctx, applicationID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		row, err := q.LockMobileInstallation(ctx, sqlc.LockMobileInstallationParams{ApplicationID: applicationID, PublicID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		binding, err := q.LockActiveInstallationBinding(ctx, sqlc.LockActiveInstallationBindingParams{ApplicationID: applicationID, InstallationID: row.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if binding.ID != bindingID || binding.OwnerEpoch != ownerEpoch {
			return nil
		}
		if _, err := q.RetireInstallationBinding(ctx, sqlc.RetireInstallationBindingParams{ApplicationID: applicationID, ID: bindingID}); err != nil {
			return err
		}
		_, err = q.AdvanceMobileInstallation(ctx, sqlc.AdvanceMobileInstallationParams{ApplicationID: applicationID, ID: row.ID,
			OwnerUserID: row.OwnerUserID, OwnerEpoch: row.OwnerEpoch, ExpectedVersion: row.Version})
		changed = err == nil
		return err
	})
	return changed && err == nil, err
}
