// Command dashboard serves the staff browser application and its session BFF.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"hello/internal/dashboard"
)

func main() {
	if err := run(); err != nil {
		log.Print("dashboard stopped: ", err)
		os.Exit(1)
	}
}
func run() error {
	var runtime dashboard.Runtime
	var err error
	switch {
	case len(os.Args) == 2 && os.Args[1] == "--deployment":
		runtime, err = dashboard.LoadDeployment()
	case len(os.Args) == 1:
		runtime, err = dashboard.Load(os.Getenv("HELLO_DASHBOARD_CONFIG"))
	default:
		return errors.New("invalid dashboard configuration")
	}
	if err != nil {
		return errors.New("invalid protected dashboard configuration")
	}
	bff, err := dashboard.New(runtime.Config, runtime.Client)
	if err != nil {
		return errors.New("cannot initialize dashboard")
	}
	mux := http.NewServeMux()
	mux.Handle("/bff/", bff)
	mux.Handle("POST /bff/login", bff)
	mux.Handle("POST /bff/logout", bff)
	if runtime.Assets != "" {
		if _, err := os.Stat(filepath.Join(runtime.Assets, "index.html")); err != nil {
			return errors.New("dashboard assets unavailable")
		}
		files := http.FileServer(http.Dir(runtime.Assets))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; font-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'")
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				files.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			http.ServeFile(w, r, filepath.Join(runtime.Assets, "index.html"))
		})
	}
	server := &http.Server{Addr: runtime.ListenAddress, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if runtime.Gateway != nil {
		return runtime.Serve(ctx, mux)
	}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("dashboard listener failed")
		}
		return nil
	case <-ctx.Done():
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if server.Shutdown(deadline) != nil {
			_ = server.Close()
			return errors.New("dashboard shutdown deadline exceeded")
		}
		<-done
		return nil
	}
}
