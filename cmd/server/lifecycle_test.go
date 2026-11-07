package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type observedListener struct {
	net.Listener
	closed chan struct{}
}

func (l *observedListener) Close() error {
	err := l.Listener.Close()
	close(l.closed)
	return err
}

// A real listener makes shutdown race an actual accepted request.
func TestHTTPDrain(t *testing.T) {
	for _, mode := range []string{"finish", "cancel and join", "hard stop"} {
		t.Run(mode, func(t *testing.T) {
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			l, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			listener := &observedListener{Listener: l, closed: make(chan struct{})}
			entered := make(chan struct{})
			exited := make(chan struct{})
			cancelled := make(chan struct{})
			release := make(chan struct{})
			free := sync.OnceFunc(func() { close(release) })
			defer free()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(exited)
				close(entered)
				if mode == "cancel and join" {
					<-r.Context().Done()
					close(cancelled)
				}
				<-release
				w.WriteHeader(http.StatusCreated)
			})
			served := make(chan error, 1)
			grace, force := 30*time.Millisecond, 2*time.Second
			switch mode {
			case "finish":
				grace = 2 * time.Second
			case "hard stop":
				force = 30 * time.Millisecond
			}
			go func() { served <- serveHTTP(ctx, listener, handler, nil, grace, force) }()
			request := make(chan int, 1)
			client := &http.Client{Timeout: 4 * time.Second}
			defer client.CloseIdleConnections()
			go func() {
				resp, err := client.Get("http://" + listener.Addr().String())
				if err != nil {
					request <- 0
					return
				}
				resp.Body.Close()
				request <- resp.StatusCode
			}()
			waitFor(t, entered)
			stop()
			waitFor(t, listener.closed)
			if mode == "cancel and join" {
				waitFor(t, cancelled)
				select {
				case err := <-served:
					t.Fatalf("server returned before cancelled handler finished: %v", err)
				default:
				}
			}
			if mode != "hard stop" {
				free()
			}
			select {
			case err = <-served:
			case <-time.After(3 * time.Second):
				t.Fatal("server failed to exit within shutdown budgets")
			}
			switch mode {
			case "finish":
				require.NoError(t, err)
				require.Equal(t, http.StatusCreated, <-request)
			case "cancel and join":
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.False(t, errors.Is(err, errHandlersBusy))
				<-request
			case "hard stop":
				require.ErrorIs(t, err, errHandlersBusy)
				free()
				<-request
			}
			waitFor(t, exited)
		})
	}
}

func waitFor(t *testing.T, event <-chan struct{}) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle event did not occur")
	}
}
