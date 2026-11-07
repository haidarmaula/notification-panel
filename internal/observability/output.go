package observability

import (
	"io"
	"log"
	"time"
)

// BoundedOutput limits an individual diagnostic write to a pollable container
// pipe or socket. PrepareCommandOutputs adopts inherited descriptors at startup.
func BoundedOutput(w io.Writer, failed func()) io.Writer {
	return &output{writer: w, failed: failed}
}

type output struct {
	writer io.Writer
	failed func()
}

func (o *output) Write(data []byte) (int, error) {
	if d, ok := o.writer.(interface{ SetWriteDeadline(time.Time) error }); ok {
		if d.SetWriteDeadline(time.Now().Add(250*time.Millisecond)) == nil {
			defer d.SetWriteDeadline(time.Time{})
		}
	}
	n, err := o.writer.Write(data)
	if err != nil && o.failed != nil {
		o.failed()
	}
	return n, err
}

// TransportLog counts HTTP server diagnostics. net/http includes remote addresses
// and arbitrary handshake data in its messages, so only the count is exported.
func (s *State) TransportLog() *log.Logger {
	return log.New(transportLog{s}, "", 0)
}

type transportLog struct{ state *State }

func (w transportLog) Write(data []byte) (int, error) {
	w.state.transportErrors.Inc()
	return len(data), nil
}
