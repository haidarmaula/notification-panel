// Package database provides the PostgreSQL connection bootstrap for every
// executable in the Notification Panel.
//
// Its scope is deliberately narrow: it knows how to open a pgx connection
// pool from a DSN, verify that the database is reachable, and hand the pool
// back to the caller. It does not own queries, repositories, or migrations —
// those live in the sqlc-generated code and the repository subpackage, both
// of which operate on the pool this package returns.
//
// # Why a pool and not a single connection
//
// pgxpool is a concurrency-safe connection pool. Every handler goroutine in
// the API server, every message-processing goroutine in the worker, and every
// tick in the scheduler can issue queries concurrently without coordinating
// access to the underlying connection; pgx handles the multiplexing. Pool
// sizing defaults are inherited from pgxpool.New and are not tuned here —
// production deployments that need explicit limits should switch to
// pgxpool.ParseConfig + pgxpool.NewWithConfig at a later layer.
//
// # Startup contract
//
// New performs an eager connectivity check before returning. Callers
// therefore get either a pool that is known to be able to reach the database,
// or an error and no pool. This makes the fail-fast pattern used in
// cmd/server, cmd/worker, cmd/scheduler, and cmd/bootstrap safe: they can
// treat a nil pool as "process cannot start" without ever holding a
// half-initialised pool.
package database

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// New opens a PostgreSQL connection pool using the supplied DSN and verifies
// that the database is reachable before returning.
//
// # Parameters
//
//   - ctx: used for both pool construction and the initial Ping. It should
//     carry a startup-appropriate deadline; the callers in this repository
//     pass context.Background() and rely on TCP-level timeouts, but a
//     bounded context (for example, five seconds) is the recommended pattern
//     for production code so that an unreachable database does not hang a
//     process during rollout.
//   - connString: the libpq-style DSN produced by
//     config.BaseConfig.GetDatabaseURL, of the form
//     "postgres://user:pass@host:port/name?sslmode=mode". It is passed
//     through to pgxpool.New verbatim; New does not validate its structure.
//     An empty or malformed DSN will surface as an error from pgxpool.New.
//
// # Lifecycle and ownership
//
// On success, ownership of the pool transfers to the caller, who is
// responsible for calling pool.Close exactly once when the pool is no longer
// needed — typically via defer at the top of main. New does not install a
// finalizer, so a pool that is never closed will hold open connections until
// the process exits. The pool is safe for concurrent use by multiple
// goroutines; callers do not need to serialise access to it.
//
// On failure at either stage, New returns a nil pool and a non-nil error. No
// cleanup is required by the caller in that case: if the ping check fails,
// the partially-initialised pool is closed inside New before the error is
// returned, so there is no connection leak.
//
// # Error conditions
//
// New returns a non-nil error in two distinct cases:
//
//   - pgxpool.New fails to parse the DSN or to establish the pool object —
//     for example, because connString is malformed or the driver cannot
//     allocate initial resources.
//   - pgxpool.Ping fails, typically because the database is unreachable,
//     rejecting authentication, or rejecting the requested sslmode. The
//     underlying pgx error is returned unwrapped so callers can inspect it
//     with errors.Is / errors.As where needed.
//
// # Concurrency
//
// New itself is safe to call from multiple goroutines, though there is rarely
// a reason to do so: each call creates an independent pool. It must not be
// invoked concurrently on the same DSN within a rolling-restart window
// without considering PostgreSQL's max_connections limit, since each pool
// reserves its own set of connections.
func New(ctx context.Context, connString string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, connString)

	if err != nil {
		return nil, err
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return pool, nil
}
