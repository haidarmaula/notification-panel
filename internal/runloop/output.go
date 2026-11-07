package runloop

import (
	"context"
	"os"
)

// InterruptOutputOnCancel closes the process-owned streams on cancellation to
// interrupt blocked pipe writes. Call the returned function before process exit
// to unregister the callback or join it if it has already started.
func InterruptOutputOnCancel(ctx context.Context, stdout, stderr *os.File) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = stdout.Close()
		_ = stderr.Close()
		close(done)
	})
	return func() {
		if !stop() {
			<-done
		}
	}
}
