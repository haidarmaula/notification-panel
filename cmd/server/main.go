// Package main implements the Notification Panel HTTP API server.
//
// It bootstraps the administrative REST API for managing users, staff,
// segments, profiles, and notification campaigns, as well as the
// mobile-facing endpoints used for device token registration and notification
// synchronisation.
//
// # Composition
//
// The server composes feature modules (auth, users, staff, profile, segments,
// notifications, mobile) on top of a shared PostgreSQL connection pool and
// installs a common middleware chain for API key validation, JWT
// authentication, role-based access control, and audit logging.
//
// The JWT middleware performs a per-request active-status check against the
// staff_users table, so the process also constructs a StaffUserRepository
// outside of the staff module and passes it to the middleware. The
// repository is a thin, stateless wrapper around the shared sqlc.Queries,
// and constructing a second instance for the middleware is intentional: it
// keeps the middleware's dependency surface small (the narrow
// StaffStatusChecker interface) without forcing the staff module to expose
// its internal repository.
//
// The users module receives the shared *pgxpool.Pool so that its
// UpdateDeviceToken path can wrap two writes in a single transaction. The
// pool is passed as the users package's narrow Transactor interface, so the
// users service remains decoupled from pgxpool.
//
// # Configuration and lifecycle
//
// Configuration is loaded from the environment via config.LoadServerConfig.
// The process listens on :8080 and terminates fatally if the database cannot
// be reached or if the HTTP listener fails; the server has no meaningful
// degraded mode.
package main

import (
	"context"
	"log"
	"net/http"

	"hello/internal/config"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"

	"hello/internal/features/applications"
	"hello/internal/features/auth"
	"hello/internal/features/mobile"
	"hello/internal/features/notifications"
	"hello/internal/features/profile"
	"hello/internal/features/segments"
	"hello/internal/features/staff"
	"hello/internal/features/users"
)

// main wires together the database, token manager, middleware chain, and all
// feature modules, then starts the HTTP server on port 8080.
//
// # Error handling
//
// The function intentionally performs "fail-fast" error handling: any failure
// to connect to PostgreSQL or to bind the listener causes an immediate
// log.Fatal, since the server cannot operate in a degraded state and a
// partially-initialised process would be worse than a restart.
//
// # Dependency construction order
//
// The token manager, API key middleware, and staff-user repository are
// constructed before the JWT middleware because the middleware depends on
// the first and third of those. Feature modules are then constructed and
// registered in an order that has no semantic significance — each module
// registers its own routes on the shared *http.ServeMux, and the mux is
// order-insensitive as long as no two patterns conflict.
//
// The shared *pgxpool.Pool (db) is passed to the users module as its
// Transactor; the module uses it to make UpdateDeviceToken atomic. It is
// also used indirectly by every other module, since each module's
// repositories are built from sqlc.Queries which is itself backed by the
// same pool.
//
// # Concurrency
//
// main runs on the primary goroutine. Each feature module's RegisterRoutes
// call only mutates the local *http.ServeMux, so the setup phase is
// single-threaded and safe. Once server.ListenAndServe is invoked, net/http
// spawns a goroutine per incoming connection, and the shared dependencies
// passed into the modules (sqlc.Queries, token.Manager, middleware closures,
// the StaffUserRepository used by the JWT middleware, and the *pgxpool.Pool
// used by the users module) are safe for concurrent use.
//
// # Parameters
//
// main takes no parameters and never returns; termination is by log.Fatal on
// startup error, by the process receiving SIGKILL/SIGTERM, or by the OS.
func main() {
	cfg := config.LoadServerConfig()
	ctx := context.Background()

	db, err := database.New(ctx, cfg.GetDatabaseURL())
	queries := sqlc.New(db)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	tokenManager := token.NewTokenManager(cfg.AccessSecret, cfg.RefreshSecret)

	// The JWT middleware re-reads each authenticated staff user's active
	// status on every request, so it needs a StaffUserRepository in
	// addition to the token manager. The repository is a stateless
	// wrapper around the shared sqlc.Queries; constructing a second
	// instance for the middleware is intentional and does not duplicate
	// state.
	staffRepoForJWT := repository.NewStaffUserRepository(queries)

	apiKeyMW := middleware.NewAPIKeyMiddleware(cfg.APIKey)
	jwtMW := middleware.NewJWTMiddleware(tokenManager, staffRepoForJWT)
	mobileJWTMW := middleware.NewMobileJWTMiddleware(cfg.MobileJWTSecret)
	auditMW := middleware.NewAuditMiddleware()

	mux := http.NewServeMux()

	mobileModule := mobile.NewMobileModule(queries, cfg)
	mobileModule.RegisterRoutes(mux)

	// db is passed to the users module as its Transactor so that
	// UpdateDeviceToken can wrap its two writes in a single database
	// transaction. The pool satisfies the users.Transactor interface
	// structurally.
	userModule := users.NewUserModule(
		queries,
		db,
		[]middleware.Middleware{apiKeyMW.Use, jwtMW.Use},
		[]middleware.Middleware{mobileJWTMW.Use},
	)
	userModule.RegisterRoutes(mux)

	authModule := auth.NewAuthModule(queries, tokenManager, apiKeyMW.Use)
	authModule.RegisterRoutes(mux)

	applications.NewModule(queries, db, apiKeyMW.Use, jwtMW.Use, auditMW.Use).RegisterRoutes(mux)

	staffModule := staff.NewStaffModule(queries, apiKeyMW.Use, jwtMW.Use, auditMW.Use)
	staffModule.RegisterRoutes(mux)

	profileModule := profile.NewProfileModule(queries, apiKeyMW.Use, jwtMW.Use)
	profileModule.RegisterRoutes(mux)

	segmentModule := segments.NewSegmentModule(queries, apiKeyMW.Use, jwtMW.Use, auditMW.Use)
	segmentModule.RegisterRoutes(mux)

	notificationModule := notifications.NewNotificationModule(queries, cfg, apiKeyMW.Use, jwtMW.Use, auditMW.Use)
	notificationModule.RegisterRoutes(mux)

	server := &http.Server{
		Addr:    ":8080",
		Handler: middleware.Logging(mux),
	}

	log.Println("server running :8080")
	log.Fatal(server.ListenAndServe())
}
