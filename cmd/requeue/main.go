// Command requeue inspects or replays explicitly scoped quarantine positions.
package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"hello/internal/dispatch"
	"hello/internal/requeue"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// Broken stdout must return a write error so the session can drain its writer.
	signal.Ignore(syscall.SIGPIPE)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, openDeploymentSession)
	stop()
	os.Exit(code)
}

func openDeploymentSession(ctx context.Context, r dispatch.ReplayRequest, credential string, execute bool) (requeue.Session, error) {
	cfg, err := requeue.LoadDeploymentConfiguration()
	if err != nil {
		return nil, err
	}
	return cfg.Open(ctx, r, credential, execute)
}

func run(ctx context.Context, args []string, input, output *os.File, diagnostics io.Writer, open requeue.OpenFunc) int {
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		// These streams belong to this one-shot command. Closing pollable pipes
		// interrupts incomplete input and blocked reports before session shutdown.
		_ = input.Close()
		_ = output.Close()
		close(interrupted)
	})
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
	return requeue.Run(ctx, args, input, output, diagnostics, open)
}
