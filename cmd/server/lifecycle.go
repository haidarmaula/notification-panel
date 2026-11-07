package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

var errHandlersBusy = errors.New("HTTP handlers did not stop after forced shutdown")

// serveHTTP owns the listener. On shutdown it allows active requests to finish
// before cancelling their contexts and closing their sockets. A forced drain
// is reported even when all interrupted handlers subsequently exit.
func serveHTTP(ctx context.Context, listener net.Listener, handler http.Handler, grace, force time.Duration) error {
	requests, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRequests()
	active := &httpHandlers{handler: handler, idle: make(chan struct{})}
	server := &http.Server{
		Handler:           active,
		BaseContext:       func(net.Listener) context.Context { return requests },
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	var serveErr error
	serveStopped := false
	select {
	case <-ctx.Done():
	case serveErr = <-served:
		serveStopped = true
	}
	drain, stop := context.WithTimeout(context.Background(), grace)
	drainErr := server.Shutdown(drain)
	stop()
	if !serveStopped {
		serveErr = <-served
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	if drainErr == nil {
		return serveErr
	}
	// Close can return before an active handler finishes. Seal admission before
	// waiting so a request racing the forced close cannot acquire the pool.
	idle := active.seal()
	cancelRequests()
	closeErr := server.Close()
	timer := time.NewTimer(force)
	defer timer.Stop()
	select {
	case <-idle:
		return errors.Join(serveErr, drainErr, closeErr)
	case <-timer.C:
		return errors.Join(serveErr, drainErr, closeErr, errHandlersBusy)
	}
}

type httpHandlers struct {
	handler http.Handler
	mu      sync.Mutex
	count   int
	sealed  bool
	idle    chan struct{}
}

func (h *httpHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if h.sealed {
		h.mu.Unlock()
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}
	h.count++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.count--
		if h.sealed && h.count == 0 {
			close(h.idle)
		}
	}()
	h.handler.ServeHTTP(w, r)
}

func (h *httpHandlers) seal() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sealed = true
	if h.count == 0 {
		close(h.idle)
	}
	return h.idle
}
