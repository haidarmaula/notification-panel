// Command provisioner accepts reserved FCM credential stages over private mTLS.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"hello/internal/deployment"
	"hello/internal/provisioning"
	"hello/internal/runloop"
)

const provisionerPolicyPath = "/etc/hello/provisioner.json"

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "provisioner accepts no arguments")
		return 2
	}
	signal.Ignore(syscall.SIGPIPE)
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer runloop.InterruptOutputOnCancel(ctx, os.Stdout, os.Stderr)()
	return process(ctx)
}

func process(ctx context.Context) int {
	data, err := deployment.ReadSecret(provisionerPolicyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid provisioner configuration")
		return 1
	}
	policy, err := provisioning.LoadPolicy(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid provisioner configuration")
		return 1
	}
	if err := policy.Run(ctx); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "provisioner unavailable")
		return 1
	}
	return 0
}
