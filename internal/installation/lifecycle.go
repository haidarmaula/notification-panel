package installation

import (
	"bytes"
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

var (
	ErrNotFound        = errors.New("installation not found")
	ErrProjectInactive = errors.New("installation project is not active")
	ErrVersion         = errors.New("installation version conflict")
	ErrAddressClaimed  = errors.New("provider address already claimed")
	ErrTransition      = errors.New("invalid installation transition")
)

type Transactor interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type Lifecycle struct {
	db        Transactor
	protector *Protector
}

func NewLifecycle(db Transactor, protector *Protector) (*Lifecycle, error) {
	if db == nil || protector == nil {
		return nil, ErrConfiguration
	}
	return &Lifecycle{db: db, protector: protector}, nil
}

// Principal is supplied by the trusted mobile authority after JWT and project
// verification. Enrollment additionally requires verified app origin.
type Principal struct {
	ApplicationID int64
	UserID        int64
}

// Route is resolved by server policy. The address is opaque client input.
type Route struct {
	Namespace   string
	Key         string
	Platform    string
	AddressType string
}

type State struct {
	PublicID   pgtype.UUID
	Version    int64
	OwnerEpoch int64
}

type Enrollment struct {
	State State
	Proof string
}

type Transition string

const (
	Refresh     Transition = "refresh"
	Logout      Transition = "logout"
	SwitchOwner Transition = "switch_owner"
)

type Change struct {
	InstallationID  pgtype.UUID
	Proof           string
	ExpectedVersion int64
	Operation       Transition
	Route           Route
	Address         string
}

func installationState(row sqlc.MobileInstallation) State {
	return State{PublicID: row.PublicID, Version: row.Version, OwnerEpoch: row.OwnerEpoch}
}

func requireLifecycleActor(ctx context.Context, q *sqlc.Queries, actor Principal) error {
	if actor.ApplicationID <= 0 || actor.UserID <= 0 {
		return ErrNotFound
	}
	status, err := q.LockInstallationProject(ctx, actor.ApplicationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "ACTIVE" {
		return ErrProjectInactive
	}
	_, err = q.LockInstallationUser(ctx, sqlc.LockInstallationUserParams{ApplicationID: actor.ApplicationID, UserID: actor.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *Lifecycle) Enroll(ctx context.Context, actor Principal, route Route, address string) (Enrollment, error) {
	var result Enrollment
	err := s.write(ctx, func(q *sqlc.Queries) error {
		if err := requireLifecycleActor(ctx, q, actor); err != nil {
			return err
		}
		proof, verifier, err := NewProof()
		if err != nil {
			return err
		}
		row, err := q.CreateMobileInstallation(ctx, sqlc.CreateMobileInstallationParams{ApplicationID: actor.ApplicationID, OwnerUserID: actor.UserID, ProofVerifier: verifier[:]})
		if err != nil {
			return err
		}
		if err := s.insertBinding(ctx, q, row, route, address); err != nil {
			return err
		}
		result = Enrollment{State: installationState(row), Proof: proof}
		return nil
	})
	if err != nil {
		return Enrollment{}, err
	}
	return result, nil
}

func (s *Lifecycle) Apply(ctx context.Context, actor Principal, change Change) (State, error) {
	switch change.Operation {
	case Refresh, Logout, SwitchOwner:
	default:
		return State{}, ErrTransition
	}
	var result State
	err := s.write(ctx, func(q *sqlc.Queries) error {
		if err := requireLifecycleActor(ctx, q, actor); err != nil {
			return err
		}
		row, err := q.LockMobileInstallation(ctx, sqlc.LockMobileInstallationParams{ApplicationID: actor.ApplicationID, PublicID: change.InstallationID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		sameOwner := row.OwnerUserID.Valid && row.OwnerUserID.Int64 == actor.UserID
		if !VerifyProof(change.Proof, row.ProofVerifier) || (change.Operation != SwitchOwner && !sameOwner) {
			return ErrNotFound
		}
		if row.Version != change.ExpectedVersion {
			return ErrVersion
		}
		if change.Operation == SwitchOwner && sameOwner {
			return ErrTransition
		}
		binding, err := q.LockActiveInstallationBinding(ctx, sqlc.LockActiveInstallationBindingParams{ApplicationID: row.ApplicationID, InstallationID: row.ID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			if change.Operation == Refresh && matchesRoute(binding, change.Route) {
				address, err := s.protector.Open(BindingIdentity{row.ApplicationID, row.ID, binding.ID}, binding.EncryptionKeyVersion, binding.AddressCiphertext)
				if err != nil {
					return err
				}
				equal := bytes.Equal(address, []byte(change.Address))
				clear(address)
				if equal {
					result = installationState(row)
					return q.TouchMobileInstallation(ctx, sqlc.TouchMobileInstallationParams{ApplicationID: row.ApplicationID, ID: row.ID})
				}
			}
			n, err := q.RetireInstallationBinding(ctx, sqlc.RetireInstallationBindingParams{ApplicationID: row.ApplicationID, ID: binding.ID})
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrVersion
			}
		}

		owner, epoch := row.OwnerUserID, row.OwnerEpoch
		switch change.Operation {
		case Logout:
			owner = pgtype.Int8{}
			epoch++
		case SwitchOwner:
			owner = pgtype.Int8{Int64: actor.UserID, Valid: true}
			epoch++
		}
		updated, err := q.AdvanceMobileInstallation(ctx, sqlc.AdvanceMobileInstallationParams{ApplicationID: row.ApplicationID, ID: row.ID, ExpectedVersion: row.Version, OwnerUserID: owner, OwnerEpoch: epoch})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVersion
		}
		if err != nil {
			return err
		}
		if change.Operation != Logout {
			if err := s.insertBinding(ctx, q, updated, change.Route, change.Address); err != nil {
				return err
			}
		}
		result = installationState(updated)
		return nil
	})
	if err != nil {
		return State{}, err
	}
	return result, nil
}

func matchesRoute(binding sqlc.InstallationBinding, route Route) bool {
	return binding.ProviderNamespace == route.Namespace && binding.ProviderRouteKey == route.Key &&
		binding.Platform == route.Platform && binding.AddressType == route.AddressType
}

func (s *Lifecycle) insertBinding(ctx context.Context, q *sqlc.Queries, row sqlc.MobileInstallation, route Route, address string) error {
	if len(route.Key) == 0 || len(route.Key) > 255 || (route.Platform != "ANDROID" && route.Platform != "IOS") {
		return ErrAddress
	}
	id, err := q.ReserveInstallationBindingID(ctx)
	if err != nil {
		return err
	}
	protected, err := s.protector.Protect(BindingIdentity{row.ApplicationID, row.ID, id}, route.Namespace, route.AddressType, address)
	if err != nil {
		return err
	}
	err = q.InsertInstallationBinding(ctx, sqlc.InsertInstallationBindingParams{
		ID: id, ApplicationID: row.ApplicationID, InstallationID: row.ID, OwnerUserID: row.OwnerUserID.Int64,
		OwnerEpoch: row.OwnerEpoch, Platform: route.Platform, ProviderNamespace: route.Namespace, ProviderRouteKey: route.Key,
		AddressType: route.AddressType, AddressFingerprint: protected.Fingerprint[:], AddressCiphertext: protected.Ciphertext,
		EncryptionKeyVersion: protected.KeyVersion,
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_active_provider_address" {
		return ErrAddressClaimed
	}
	return err
}

func (s *Lifecycle) write(ctx context.Context, fn func(*sqlc.Queries) error) error {
	for attempt := 0; attempt < 3; attempt++ {
		err := s.attempt(ctx, fn)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || (pgErr.Code != "40001" && pgErr.Code != "40P01") || ctx.Err() != nil || attempt == 2 {
			return err
		}
	}
	panic("unreachable")
}

func (s *Lifecycle) attempt(ctx context.Context, fn func(*sqlc.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := fn(sqlc.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
