// Command scheduler runs the scoped durable scheduler.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"hello/internal/daemon"
	"hello/internal/deployment"
)

func main() {
	os.Exit(run())
}

func run() int {
	signal.Ignore(syscall.SIGPIPE)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return daemon.Process(ctx, deployment.Scheduler, os.Args[1:], os.Stdout, os.Stderr)
}
