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

// Transactor begins database transactions owned and closed by the lifecycle service.
type Transactor interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// Lifecycle applies atomic installation transitions and rechecks authority for mobile operations.
type Lifecycle struct {
	db        Transactor
	protector *Protector
}

// NewLifecycle requires a transaction source and configured address protector.
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

// Route is trusted server policy for the provider namespace, route and address format.
type Route struct {
	Namespace   string
	Key         string
	Platform    string
	AddressType string
}

// State identifies the installation and the version and owner epoch used to fence changes.
type State struct {
	PublicID   pgtype.UUID
	Version    int64
	OwnerEpoch int64
}

// Enrollment returns committed installation state and the newly issued secret proof.
// Callers must keep Proof out of logs and subsequent state responses.
type Enrollment struct {
	State State
	Proof string
}

// Transition names a refresh, logout or account switch guarded by proof and version.
type Transition string

const (
	Refresh     Transition = "refresh"
	Logout      Transition = "logout"
	SwitchOwner Transition = "switch_owner"
)

// Change supplies an installation proof and expected version for a lifecycle transition.
// Route must come from trusted server policy; logout ignores Route and Address.
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

func requireLifecycleActor(ctx context.Context, q *sqlc.Queries, actor Principal, session *MobileSession) error {
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
	if err == nil && session != nil {
		return session.LockIdentity(ctx, q, session.project)
	}
	return err
}

// Enroll atomically creates an installation and its first active binding for trusted internal callers.
// The actor must already have verified enrollment origin and the route must be server-selected.
// Mounted mobile routes use EnrollSession to retain the bearer identity through commit.
// Proof is returned only after commit; an active address collision rolls back creation.
func (s *Lifecycle) Enroll(ctx context.Context, actor Principal, route Route, address string) (Enrollment, error) {
	return s.enroll(ctx, actor, nil, route, address)
}

// EnrollSession retains the verified origin and bearer identity through commit.
func (s *Lifecycle) EnrollSession(ctx context.Context, session MobileSession, route Route, address string) (Enrollment, error) {
	if !session.enrollmentVerified {
		return Enrollment{}, ErrMobileOrigin
	}
	return s.enroll(ctx, session.Principal(), &session, route, address)
}

func (s *Lifecycle) enroll(ctx context.Context, actor Principal, session *MobileSession, route Route, address string) (Enrollment, error) {
	var result Enrollment
	err := s.write(ctx, session, func(ctx context.Context, q *sqlc.Queries) error {
		if err := requireLifecycleActor(ctx, q, actor, session); err != nil {
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

// Apply checks current authority, proof and version before changing an installation.
// Refresh and logout require the current owner; switching uses the verified destination user.
// An unchanged refresh touches last-seen time without advancing the version.
func (s *Lifecycle) Apply(ctx context.Context, actor Principal, change Change) (State, error) {
	return s.apply(ctx, actor, nil, change)
}

// ApplySession rechecks the authenticated subject mapping before changing ownership or binding.
func (s *Lifecycle) ApplySession(ctx context.Context, session MobileSession, change Change) (State, error) {
	return s.apply(ctx, session.Principal(), &session, change)
}

func (s *Lifecycle) apply(ctx context.Context, actor Principal, session *MobileSession, change Change) (State, error) {
	switch change.Operation {
	case Refresh, Logout, SwitchOwner:
	default:
		return State{}, ErrTransition
	}
	var result State
	err := s.write(ctx, session, func(ctx context.Context, q *sqlc.Queries) error {
		if err := requireLifecycleActor(ctx, q, actor, session); err != nil {
			return err
		}
		row, err := q.LockMobileInstallation(ctx, sqlc.LockMobileInstallationParams{ApplicationID: actor.ApplicationID, PublicID: change.InstallationID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if session != nil {
			if err := session.LockIdentity(ctx, q, session.project); err != nil {
				return err
			}
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
				identity := BindingIdentity{
					ApplicationID: row.ApplicationID, InstallationID: row.ID, BindingID: binding.ID,
				}
				address, err := s.protector.Open(identity, binding.EncryptionKeyVersion, binding.AddressCiphertext)
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
	identity := BindingIdentity{ApplicationID: row.ApplicationID, InstallationID: row.ID, BindingID: id}
	protected, err := s.protector.Protect(identity, route.Namespace, route.AddressType, address)
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

func (s *Lifecycle) write(ctx context.Context, session *MobileSession, fn func(context.Context, *sqlc.Queries) error) error {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		err := s.attempt(ctx, session, fn)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || (pgErr.Code != "40001" && pgErr.Code != "40P01") || ctx.Err() != nil || attempt == 2 {
			return err
		}
	}
	panic("unreachable")
}

func (s *Lifecycle) attempt(ctx context.Context, session *MobileSession, fn func(context.Context, *sqlc.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		// Cleanup needs an uncanceled budget even after the operation expires.
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New(tx)
	if err := fn(ctx, q); err != nil {
		return err
	}
	// The identity lock is already held. Recheck database time after any
	// installation, binding or unique-address wait before committing.
	if session != nil {
		if err := session.LockIdentity(ctx, q, session.project); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
