// Command server serves scoped business APIs and global staff administration.
package main

import (
	"context"
	"log"
	"net/http"

	"hello/internal/config"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/applications"
	"hello/internal/features/auth"
	"hello/internal/features/profile"
	"hello/internal/features/staff"
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

	server := &http.Server{
		Addr:    ":8080",
		Handler: middleware.Logging(serverRoutes(sqlc.New(db), db, cfg)),
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
