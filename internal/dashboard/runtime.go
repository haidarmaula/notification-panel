package dashboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// Serve owns and joins the authenticated gateway and diagnostics servers.
func (r Runtime) Serve(ctx context.Context, mux *http.ServeMux) error {
	if r.Gateway == nil || r.Diagnostics == nil {
		return errors.New("authenticated dashboard listeners required")
	}
	public, err := r.Gateway.Listener.Listen(ctx)
	if err != nil {
		return err
	}
	private, err := r.Diagnostics.Listen(ctx)
	if err != nil {
		_ = public.Close()
		return err
	}
	publicServer := &http.Server{Handler: r.Gateway.Protect(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384, ErrorLog: log.New(io.Discard, "", 0)}
	diagnostics := http.NewServeMux()
	diagnostics.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "live\n") })
	diagnostics.HandleFunc("GET /readyz", func(w http.ResponseWriter, request *http.Request) {
		check, cancel := context.WithTimeout(request.Context(), 3*time.Second)
		defer cancel()
		upstream, err := http.NewRequestWithContext(check, http.MethodGet, r.Upstream+"/api/v1/apps", nil)
		if err != nil {
			http.Error(w, "unready", 503)
			return
		}
		upstream.Header.Set("X-API-Key", r.APIKey)
		upstream.Header.Set("X-Forwarded-For", "127.0.0.1")
		upstream.Header.Set("X-Forwarded-Proto", "https")
		response, err := r.Client.Do(upstream)
		if err != nil {
			http.Error(w, "unready", 503)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != 401 {
			http.Error(w, "unready", 503)
			return
		}
		_, _ = io.WriteString(w, "ready\n")
	})
	diagnostics.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintln(w, "hello_dashboard_process_up 1")
	})
	privateServer := &http.Server{Handler: diagnostics, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	done := make(chan error, 2)
	go func() { done <- publicServer.Serve(public) }()
	go func() { done <- privateServer.Serve(private) }()
	consumed := 0
	failed := false
	select {
	case <-ctx.Done():
	case <-done:
		consumed++
		failed = true
	}
	deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if publicServer.Shutdown(deadline) != nil {
		_ = publicServer.Close()
		failed = true
	}
	if privateServer.Shutdown(deadline) != nil {
		_ = privateServer.Close()
		failed = true
	}
	for consumed < 2 {
		<-done
		consumed++
	}
	if failed {
		return errors.New("dashboard listener stopped unexpectedly")
	}
	return nil
}
