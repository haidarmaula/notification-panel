package daemon

import (
	"context"
	"os"

	"hello/internal/deployment"
)

// Process owns the executable's output streams for its lifetime. Cancellation
// interrupts blocked pipe writes so logging cannot prevent joined shutdown.
// Command remains available for callers that own cancellation-aware writers.
func Process(ctx context.Context, role deployment.Component, args []string, stdout, stderr *os.File) int {
	if stdout == nil || stderr == nil {
		return 2
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = stdout.Close()
		_ = stderr.Close()
		close(interrupted)
	})
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
	return Command(ctx, role, args, stdout, stderr)
}
