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

func main() {
	cfg := config.Load()
	ctx := context.Background()

	db, err := database.New(ctx, cfg.DatabaseURL())
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
