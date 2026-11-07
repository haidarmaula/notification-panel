package daemon

import (
	"context"
	"os"

	"hello/internal/runloop"

	"hello/internal/deployment"
)

// Process owns the executable's output streams for its lifetime. Cancellation
// interrupts blocked pipe writes so logging cannot prevent joined shutdown.
// Command remains available for callers that own cancellation-aware writers.
func Process(ctx context.Context, role deployment.Component, args []string, stdout, stderr *os.File) int {
	if stdout == nil || stderr == nil {
		return 2
	}
	defer runloop.InterruptOutputOnCancel(ctx, stdout, stderr)()
	return Command(ctx, role, args, stdout, stderr)
}
