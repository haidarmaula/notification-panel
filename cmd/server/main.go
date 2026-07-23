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
	superAdminMW := middleware.NewSuperAdminMiddleware(cfg.SuperAdminRole)

	mux := http.NewServeMux()

	mobileModule := mobile.NewMobileModule(queries, cfg)
	mobileModule.RegisterRoutes(mux)

	userModule := users.NewUserModule(queries, apiKeyMW.Use, jwtMW.Use)
	userModule.RegisterRoutes(mux)

	authModule := auth.NewAuthModule(queries, tokenManager, apiKeyMW.Use)
	authModule.RegisterRoutes(mux)

	staffModule := staff.NewStaffModule(queries, apiKeyMW.Use, jwtMW.Use, superAdminMW.Use, middleware.AuditMiddleware)
	staffModule.RegisterRoutes(mux)

	profileModule := profile.NewProfileModule(queries, apiKeyMW.Use, jwtMW.Use)
	profileModule.RegisterRoutes(mux)

	segmentModule := segments.NewSegmentModule(queries, apiKeyMW.Use, jwtMW.Use)
	segmentModule.RegisterRoutes(mux)

	notificationModule := notifications.NewNotificationModule(queries, cfg, apiKeyMW.Use, jwtMW.Use)
	notificationModule.RegisterRoutes(mux)

	server := &http.Server{
		Addr:    ":8080",
		Handler: middleware.Logging(mux),
	}

	log.Println("server running :8080")
	log.Fatal(server.ListenAndServe())
}
