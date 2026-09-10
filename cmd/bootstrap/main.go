// Package main implements the one-shot bootstrap utility for the Notification
// Panel.
//
// It is intended to be executed manually or from a deployment job (e.g. a
// Kubernetes init container or a `make bootstrap` target) after database
// migrations have been applied. Its responsibilities are:
//
//   - Ensuring the canonical set of role rows exists (e.g. SUPER_ADMIN).
//   - Creating the initial super-admin staff account if it has not yet been
//     provisioned.
//
// # Idempotency
//
// The bootstrap routine is idempotent: running it multiple times is safe and
// will not duplicate data or overwrite existing credentials. It exits with a
// non-zero status if any step fails so that automated pipelines can detect
// provisioning errors.
//
// # Configuration
//
// Configuration is loaded from the environment via config.LoadBootstrapConfig,
// which returns a superset of the server and worker configuration — including
// the super-admin role name and the credentials for the initial admin account.
package main

import (
	"context"
	"log"

	"hello/internal/bootstrap"
	"hello/internal/config"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
)

// main loads configuration, connects to PostgreSQL, constructs the role and
// staff-user repositories, and runs the bootstrap routine.
//
// # Error handling
//
// The process terminates fatally on any error: connection failure, repository
// construction failure, or an error reported by bootstrap.Run. On success it
// logs "bootstrap completed" and exits 0.
//
// # Concurrency
//
// main is single-threaded and does not spawn goroutines. It is intended to be
// invoked as a one-shot job, not as a long-running service. Concurrent
// invocation alongside a running scheduler or worker is safe because the
// underlying bootstrap routine is idempotent at the database level.
func main() {
	cfg := config.LoadBootstrapConfig()
	ctx := context.Background()

	db, err := database.New(ctx, cfg.GetDatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	queries := sqlc.New(db)

	roleRepo := repository.NewRoleRepository(queries)
	staffRepo := repository.NewStaffUserRepository(queries)

	bs := bootstrap.New(
		roleRepo,
		staffRepo,
		cfg,
	)

	if err := bs.Run(context.Background()); err != nil {
		log.Fatal(err)
	}

	log.Println("bootstrap completed")
}
