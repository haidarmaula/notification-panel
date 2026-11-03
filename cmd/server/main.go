// Command server serves scoped business APIs and global staff administration.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"hello/internal/config"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/applications"
	"hello/internal/features/auth"
	"hello/internal/features/profile"
	"hello/internal/features/staff"
	"hello/internal/installation"
	"hello/internal/middleware"
	"hello/internal/token"
)

func main() {
	cfg := config.LoadServerConfig()
	db, err := database.New(context.Background(), cfg.GetDatabaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	queries := sqlc.New(db)
	mux := serverRoutes(queries, db, cfg)
	if path := os.Getenv("MOBILE_POLICY_FILE"); path != "" {
		file, err := os.Open(path)
		if err != nil {
			log.Fatal("cannot open mobile policy file")
		}
		runtime, err := installation.LoadRuntime(file, queries, db)
		file.Close()
		if err != nil {
			log.Fatal("invalid mobile policy configuration")
		}
		applications.RegisterMobileLifecycle(mux, runtime)
	}

	server := &http.Server{
		Addr:    ":8080",
		Handler: middleware.Logging(mux),
	}
	log.Println("server running :8080")
	log.Fatal(server.ListenAndServe())
}

func serverRoutes(queries *sqlc.Queries, db applications.Transactor, cfg *config.ServerConfig) *http.ServeMux {
	tokenManager := token.NewTokenManager(cfg.AccessSecret, cfg.RefreshSecret)
	apiKeyMW := middleware.NewAPIKeyMiddleware(cfg.APIKey)
	jwtMW := middleware.NewJWTMiddleware(tokenManager, repository.NewStaffUserRepository(queries))
	auditMW := middleware.NewAuditMiddleware()

	mux := http.NewServeMux()
	auth.NewAuthModule(queries, tokenManager, apiKeyMW.Use).RegisterRoutes(mux)
	applications.NewModule(queries, db, apiKeyMW.Use, jwtMW.Use, auditMW.Use).RegisterRoutes(mux)
	staff.NewStaffModule(queries, apiKeyMW.Use, jwtMW.Use, auditMW.Use).RegisterRoutes(mux)
	profile.NewProfileModule(queries, apiKeyMW.Use, jwtMW.Use).RegisterRoutes(mux)
	return mux
}
