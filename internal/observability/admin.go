package observability

import (
	"context"

	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"hello/internal/deployment"
)

var errAdmin = errors.New("invalid private diagnostics configuration")

// Admin is a diagnostics listener with a dedicated client trust bundle.
type Admin = deployment.PrivateListener

// ParseAdmin leaves diagnostics disabled when the optional role setting is absent.
func ParseAdmin(data json.RawMessage) (*Admin, error) {
	if len(data) == 0 {
		return nil, nil
	}
	a, err := deployment.ParsePrivateListener(data)
	if err != nil {
		return nil, errAdmin
	}
	return a, nil
}

func servePrivate(ctx context.Context, l net.Listener, h http.Handler) error {
	server := &http.Server{Handler: h, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 5 * time.Second,
		IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	joined := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = server.Close(); close(joined) })
	err := server.Serve(l)
	if !stop() {
		<-joined
	}
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return errors.New("private diagnostics listener stopped")
}
