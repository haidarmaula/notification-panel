package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"hello/internal/deployment"
	"hello/internal/observability"
)

// Command runs the compiled process role with its fixed protected policy. Only
// help is accepted on argv; logs go to stderr and never contain policy values.
// It returns after process work and owned resources have been joined/closed.
func Command(ctx context.Context, role deployment.Component, args []string, stdout, stderr io.Writer) int {
	return command(ctx, role, args, stdout, stderr, LoadDeployment, (*Configuration).Run)
}

func command(ctx context.Context, role deployment.Component, args []string, stdout, stderr io.Writer,
	load func(deployment.Component) (*Configuration, error), run func(*Configuration, context.Context, *slog.Logger) error,
) int {
	if !daemonRole(role) || stdout == nil || stderr == nil || load == nil || run == nil {
		return 2
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		if _, err := fmt.Fprintf(stdout, "Usage: %s [--help]\nPolicy: /etc/hello/%s.json (Darwin: /private/etc/hello/%s.json)\n", role, role, role); err != nil {
			return 1
		}
		return 0
	}
	if len(args) != 0 {
		_, _ = io.WriteString(stderr, "invalid arguments; use --help\n")
		return 2
	}
	logger := slog.New(slog.NewJSONHandler(observability.BoundedOutput(stderr, nil), nil)).With("component", string(role))
	if ctx.Err() != nil {
		return 0
	}
	cfg, err := load(role)
	if err != nil || cfg == nil || cfg.role != role {
		logger.ErrorContext(ctx, "deployment policy rejected")
		return 1
	}
	err = run(cfg, ctx, logger)
	// Only a clean cancellation is normal. A joined producer-drain failure must
	// remain a failure even when cancellation is also present in the error tree.
	if err == nil || (ctx.Err() != nil && (err == ctx.Err() || err == context.Cause(ctx))) {
		return 0
	}
	logger.ErrorContext(ctx, "daemon stopped with failure")
	return 1
}
