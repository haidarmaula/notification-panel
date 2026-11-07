// Command bootstrap provisions the initial super-admin after migrations.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hello/internal/bootstrap"
	"hello/internal/config"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
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
	cfg, err := config.BootstrapForCommand(os.Args[1:])
	if err != nil {
		log.Print("invalid bootstrap configuration")
		return 1
	}
	if err := provision(ctx, cfg); err != nil {
		log.Print(err)
		return 1
	}
	log.Print("bootstrap completed")
	return 0
}

func provision(ctx context.Context, cfg *config.BootstrapConfig) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	startup, stopStartup := context.WithTimeout(ctx, 15*time.Second)
	db, err := cfg.OpenDatabase(startup)
	stopStartup()
	if err != nil {
		return errors.New("bootstrap database startup failed")
	}
	defer db.Close()

	queries := sqlc.New(db)
	bs := bootstrap.New(repository.NewRoleRepository(queries), repository.NewStaffUserRepository(queries), cfg)
	if err := bs.Run(ctx); err != nil {
		return errors.New("bootstrap provisioning failed")
	}
	return nil
}
