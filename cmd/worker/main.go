// Command worker runs the admission or delivery role selected by protected policy.
package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"syscall"

	"hello/internal/daemon"
	"hello/internal/deployment"
	"hello/internal/runloop"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	signal.Ignore(syscall.SIGPIPE)
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if len(args) == 1 && args[0] == "--check-delivery-policy" {
		defer runloop.InterruptOutputOnCancel(ctx, os.Stdout, os.Stderr)()
		result, err := daemon.CheckDeliveryDeployment(ctx)
		if err != nil || ctx.Err() != nil {
			_, _ = io.WriteString(os.Stderr, "delivery policy preflight failed\n")
			return 1
		}
		if json.NewEncoder(os.Stdout).Encode(result) != nil {
			return 1
		}
		return 0
	}
	return daemon.Process(ctx, deployment.Worker, args, os.Stdout, os.Stderr)
}
