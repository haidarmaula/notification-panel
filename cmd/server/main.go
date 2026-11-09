// Command server serves scoped business APIs and global staff administration.
package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hello/internal/config"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/applications"
	"hello/internal/features/auth"
	"hello/internal/features/profile"
	"hello/internal/features/staff"
	"hello/internal/installation"
	"hello/internal/middleware"
	"hello/internal/observability"
	"hello/internal/runloop"
)

func main() { os.Exit(run()) }

func run() int {
	signal.Ignore(syscall.SIGPIPE)
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer runloop.InterruptOutputOnCancel(ctx, os.Stdout, os.Stderr)()
	cfg, err := config.ServerForCommand(os.Args[1:])
	if err != nil {
		log.Print("invalid server configuration")
		return 1
	}
	if err := runServer(ctx, cfg); err != nil {
		log.Print(err)
		return 1
	}
	return 0
}

func runServer(ctx context.Context, cfg *config.ServerConfig) (err error) {
	startup, stopStartup := context.WithTimeout(ctx, 15*time.Second)
	db, err := cfg.OpenDatabase(startup)
	stopStartup()
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return errors.New("server database startup failed")
	}
	defer func() {
		// An uncooperative handler still owns its dependencies. The process
		// exits nonzero and the OS reclaims them on this last-resort path.
		if !errors.Is(err, errHandlersBusy) {
			db.Close()
		}
	}()

	queries := sqlc.New(db)
	mux := serverRoutes(queries, db, cfg)
	file, err := cfg.MobilePolicy()
	if err != nil {
		return errors.New("cannot open mobile policy file")
	}
	if file != nil {
		runtime, loadErr := installation.LoadRuntime(file, queries, db)
		closeErr := file.Close()
		if loadErr != nil || closeErr != nil {
			return errors.New("invalid mobile policy configuration")
		}
		applications.RegisterMobileLifecycle(mux, runtime)
		if err := applications.RegisterMobileReceipts(mux, runtime, db); err != nil {
			return errors.New("invalid mobile receipt configuration")
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	state := observability.New()
	logger := slog.New(slog.NewJSONHandler(observability.BoundedOutput(os.Stderr, state.Dropped), nil)).With("component", "server")
	return state.Run(ctx, cfg.Monitoring, db, logger, func(ctx context.Context) error {
		var listener net.Listener
		var handler http.Handler = mux
		if cfg.Gateway != nil {
			listener, err = cfg.Gateway.Listener.Listen(ctx)
			handler = cfg.Gateway.Protect(mux)
		} else {
			var lc net.ListenConfig
			listener, err = lc.Listen(ctx, "tcp", cfg.ListenAddress)
		}
		if err != nil {
			return errors.New("server listener startup failed")
		}
		logger.InfoContext(ctx, "public API listener started")
		return serveHTTP(ctx, listener, observability.Access(handler, state.Observe), state.TransportLog(), 10*time.Second, 5*time.Second)
	})
}

func serverRoutes(queries *sqlc.Queries, db applications.Transactor, cfg *config.ServerConfig) *http.ServeMux {
	tokenManager := cfg.StaffTokens()
	apiKeyMW := middleware.NewAPIKeyMiddleware(cfg.APIKey)
	jwtMW := middleware.NewJWTMiddleware(tokenManager, repository.NewStaffUserRepository(queries))
	auditMW := middleware.NewAuditMiddleware()
	if cfg.Gateway != nil {
		auditMW = &middleware.AuditMiddleware{VerifiedClientIP: cfg.Gateway.ClientIP}
	}

	mux := http.NewServeMux()
	auth.NewAuthModule(queries, tokenManager, apiKeyMW.Use).RegisterRoutes(mux)
	applications.NewModule(queries, db, apiKeyMW.Use, jwtMW.Use, auditMW.Use).RegisterRoutes(mux)
	staff.NewStaffModule(queries, db, apiKeyMW.Use, jwtMW.Use, auditMW.Use).RegisterRoutes(mux)
	profile.NewProfileModule(queries, db, apiKeyMW.Use, jwtMW.Use).RegisterRoutes(mux)
	return mux
}
