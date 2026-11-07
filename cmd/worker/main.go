// Command worker runs the admission or delivery role selected by protected policy.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"hello/internal/daemon"
	"hello/internal/deployment"
	"hello/internal/runloop"
)

func main() {
	os.Exit(run())
}

func run() int {
	signal.Ignore(syscall.SIGPIPE)
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return daemon.Process(ctx, deployment.Worker, os.Args[1:], os.Stdout, os.Stderr)
}
