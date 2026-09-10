// Package bootstrap provides the one-shot provisioning utility that prepares
// a freshly-migrated Notification Panel database for first use.
//
// Its responsibilities are deliberately narrow:
//
//   - Ensure the canonical SUPER_ADMIN role row exists so that staff accounts
//     can be attached to a role.
//   - Create the initial super-admin staff user if (and only if) no account
//     with the configured bootstrap email already exists.
//
// The package is designed to be invoked from a deployment job — a Kubernetes
// init container, a `make bootstrap` target, or a manual operator command —
// after migrations have been applied and before the API server begins
// serving traffic. It is NOT intended to be part of the running service's
// hot path.
//
// # Idempotency
//
// Run is idempotent with respect to the configured admin email: re-running it
// with the same configuration is a no-op. Re-running it with a different
// admin email will create an additional admin account rather than rename the
// existing one. True idempotency under concurrent invocation relies on the
// database enforcing a UNIQUE constraint on staff_users.email — without that
// constraint, two racing bootstrap processes can both pass the existence
// check and both attempt an insert.
package bootstrap

import (
	"context"
	"errors"

	"hello/internal/config"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// Bootstrap coordinates the one-shot provisioning of the initial database
// state: the SUPER_ADMIN role and the first super-admin staff account.
//
// A Bootstrap value is constructed with New and executed exactly once via
// Run. It holds only references to repositories and a configuration snapshot
// and carries no mutable state of its own, so it is safe to construct cheaply
// and discard after use. Because Run performs multiple sequential database
// round-trips without an enclosing transaction, a single Bootstrap instance
// should not be executed concurrently with itself; concurrent execution is
// only safe to the extent the underlying schema enforces uniqueness on
// staff_users.email.
type Bootstrap struct {
	roleRepo  *repository.RoleRepository
	staffRepo *repository.StaffUserRepository
	cfg       *config.BootstrapConfig
}

// New constructs a Bootstrap bound to the supplied repositories and
// configuration.
//
// roleRepo and staffRepo must be non-nil; nil values are not checked and will
// panic on the first repository call inside Run. cfg must also be non-nil and
// must have its BootstrapAdmin* fields populated — Run reads them directly
// and does not validate or default them. Callers are expected to have already
// loaded a fully-populated config.BootstrapConfig via the config package.
//
// The returned value is safe to construct on every process start; the
// constructor performs no I/O.
func New(
	roleRepo *repository.RoleRepository,
	staffRepo *repository.StaffUserRepository,
	cfg *config.BootstrapConfig,
) *Bootstrap {
	return &Bootstrap{
		roleRepo:  roleRepo,
		staffRepo: staffRepo,
		cfg:       cfg,
	}
}

// Run performs the bootstrap sequence: it ensures the SUPER_ADMIN role exists,
// then creates the initial super-admin staff user if no account with the
// configured bootstrap email is already present.
//
// # Sequence and early exit
//
// The first step is a lookup of the staff user by
// cfg.BootstrapAdminEmail. If a row is found, Run returns nil immediately
// without touching the role table or creating anything. This is the
// idempotency fast path: running the utility on an already-provisioned
// database is a no-op.
//
// If the lookup returns any error other than pgx.ErrNoRows — for example, a
// connectivity failure or a malformed query — Run aborts and returns that
// error. pgx.ErrNoRows is the only error treated as "account does not exist,
// proceed to create".
//
// # Role resolution
//
// The role is looked up by the literal string "SUPER_ADMIN", not by a value
// from cfg. This is intentional but subtle: unlike the middleware layer,
// which is parameterised by cfg.SuperAdminRole, the bootstrap utility
// assumes the canonical role name. A deployment that renames the super-admin
// role will fail here with a not-found error until the literal is updated.
//
// # Password handling
//
// cfg.BootstrapAdminPassword is read in plaintext and bcrypt-hashed with
// bcrypt.DefaultCost before being written. The plaintext value never leaves
// this process; callers must ensure it is delivered to the process through a
// secure channel (environment variable injected by a secrets manager, or a
// one-shot job with restricted access) and must rotate it after first login.
//
// # Errors
//
// Run returns a non-nil error in any of the following cases:
//
//   - The initial existence check fails with an error other than
//     pgx.ErrNoRows.
//   - The SUPER_ADMIN role lookup fails (including when the role row does not
//     exist — this typically means migrations have not been applied).
//   - bcrypt fails to generate a password hash. This is rare and generally
//     indicates a password longer than bcrypt's 72-byte limit or an
//     environmental entropy problem.
//   - The staff user insert fails, for example because a concurrent bootstrap
//     process created the account first and the schema enforces a UNIQUE
//     constraint on email.
//
// Run does not retry. A failure indicates a configuration or environment
// problem that an operator should resolve before re-running the utility.
//
// # Concurrency
//
// Run performs multiple sequential database operations without an enclosing
// transaction and is therefore not safe to invoke concurrently on the same
// Bootstrap instance, nor on two instances pointing at the same database with
// the same target email. In practice the utility is invoked by a single
// deployment job, so this constraint is easy to honour; it is documented here
// so the assumption is not lost if Run is ever called from a long-running
// process.
func (b *Bootstrap) Run(ctx context.Context) error {
	_, err := b.staffRepo.FindByEmail(
		ctx,
		b.cfg.BootstrapAdminEmail,
	)

	if err == nil {
		return nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	role, err := b.roleRepo.FindByName(
		ctx,
		"SUPER_ADMIN",
	)
	if err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword(
		[]byte(b.cfg.BootstrapAdminPassword),
		bcrypt.DefaultCost,
	)
	if err != nil {
		return err
	}

	_, err = b.staffRepo.Create(
		ctx,
		sqlc.CreateStaffUserParams{
			RoleID:       role.ID,
			Name:         b.cfg.BootstrapAdminName,
			Email:        b.cfg.BootstrapAdminEmail,
			PasswordHash: string(hash),
		},
	)

	return err
}
