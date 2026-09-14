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
	"hello/internal/database/sqlc"
	"hello/internal/middleware"
	"hello/internal/token"

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
// # Concurrency
//
// main runs on the primary goroutine. Each feature module's RegisterRoutes
// call only mutates the local *http.ServeMux, so the setup phase is
// single-threaded and safe. Once server.ListenAndServe is invoked, net/http
// spawns a goroutine per incoming connection, and the shared dependencies
// passed into the modules (sqlc.Queries, token.Manager, middleware closures)
// are safe for concurrent use.
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

	apiKeyMW := middleware.NewAPIKeyMiddleware(cfg.APIKey)
	jwtMW := middleware.NewJWTMiddleware(tokenManager)
	mobileJWTMW := middleware.NewMobileJWTMiddleware(cfg.MobileJWTSecret)
	superAdminMW := middleware.NewSuperAdminMiddleware(cfg.SuperAdminRole)
	auditMW := middleware.NewAuditMiddleware()

	mux := http.NewServeMux()

	mobileModule := mobile.NewMobileModule(queries, cfg)
	mobileModule.RegisterRoutes(mux)

	userModule := users.NewUserModule(
		queries,
		[]middleware.Middleware{apiKeyMW.Use, jwtMW.Use},
		[]middleware.Middleware{mobileJWTMW.Use},
	)
	userModule.RegisterRoutes(mux)

	authModule := auth.NewAuthModule(queries, tokenManager, apiKeyMW.Use)
	authModule.RegisterRoutes(mux)

	staffModule := staff.NewStaffModule(queries, apiKeyMW.Use, jwtMW.Use, superAdminMW.Use, auditMW.Use)
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
