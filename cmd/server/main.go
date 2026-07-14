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
	"hello/internal/features/notifications"
	"hello/internal/features/profile"
	"hello/internal/features/segments"
	"hello/internal/features/staff"
	"hello/internal/features/users"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()
	db, err := database.New(ctx, cfg.DatabaseURL())
	queries := sqlc.New(db)

	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	tokenManager := token.NewTokenManager(cfg.AccessSecret, cfg.RefreshSecret)
	apiKeyMW := middleware.NewAPIKeyMiddleware(cfg.APIKey)
	jwtMW := middleware.NewJWTMiddleware(tokenManager)

	mux := http.NewServeMux()

	userModule := users.NewUserModule(queries, apiKeyMW.Use, jwtMW.Use)
	userModule.RegisterRoutes(mux)

	authModule := auth.NewAuthModule(queries, tokenManager, apiKeyMW.Use)
	authModule.RegisterRoutes(mux)

	staffModule := staff.NewStaffModule(queries, apiKeyMW.Use, jwtMW.Use, middleware.SuperAdminMiddleware)
	staffModule.RegisterRoutes(mux)

	profileModule := profile.NewProfileModule(queries, apiKeyMW.Use, jwtMW.Use)
	profileModule.RegisterRoutes(mux)

	segmentModule := segments.NewSegmentModule(queries, apiKeyMW.Use, jwtMW.Use)
	segmentModule.RegisterRoutes(mux)

	notificationModule := notifications.NewNotificationModule(queries, *cfg, apiKeyMW.Use, jwtMW.Use)
	notificationModule.RegisterRoutes(mux)

	server := &http.Server{
		Addr:    ":8080",
		Handler: middleware.Logging(mux),
	}

	log.Println("server running :8080")
	log.Fatal(server.ListenAndServe())
}
