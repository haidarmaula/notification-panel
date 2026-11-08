// Package observability provides private process diagnostics and bounded operational telemetry.
package observability

import (
	"bufio"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/felixge/httpsnoop"

	"hello/pkg/response"
)

// Request describes the externally visible HTTP outcome without client data.
type Request struct {
	ID, Method, Route, Outcome string
	Status                     int
	Bytes                      int64
	Duration                   time.Duration
}

// Access records the actual committed status and aborts a partial response on panic.
// The wrapper retains optional writer interfaces supported by the underlying server.
func Access(next http.Handler, observe func(Request)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		event := Request{ID: rand.Text(), Method: methodBucket(r.Method), Outcome: "complete"}
		w.Header().Set("X-Request-ID", event.ID)
		commit := func(status int) {
			if event.Status == 0 && (status >= 200 || status == 101) {
				event.Status = status
			}
		}
		hijacked := false
		wrapped := httpsnoop.Wrap(w, httpsnoop.Hooks{
			WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
				return func(code int) { next(code); commit(code) }
			},
			Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
				return func(b []byte) (int, error) { n, err := next(b); commit(200); event.Bytes += int64(n); return n, err }
			},
			ReadFrom: func(next httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
				return func(r io.Reader) (int64, error) { n, err := next(r); commit(200); event.Bytes += n; return n, err }
			},
			Flush: func(next httpsnoop.FlushFunc) httpsnoop.FlushFunc { return func() { next(); commit(200) } },
			Hijack: func(next httpsnoop.HijackFunc) httpsnoop.HijackFunc {
				return func() (net.Conn, *bufio.ReadWriter, error) {
					conn, rw, err := next()
					if err == nil {
						hijacked = true
					}
					return conn, rw, err
				}
			},
		})
		defer func() {
			recovered := recover()
			abort := false
			// Record the committed status even if the recovery response fails
			// to write and aborts the connection.
			defer func() {
				if event.Status == 0 && !hijacked && !abort {
					event.Status = http.StatusOK
				}
				event.Route = "unmatched"
				// ServeMux's redirect/clean-path patterns can contain raw request text.
				// Only method-qualified application registrations are route templates.
				if method, path, ok := strings.Cut(r.Pattern, " "); ok && methodBucket(method) != "other" && strings.HasPrefix(path, "/api/") {
					event.Route = path
				}
				event.Duration = time.Since(start)
				observe(event)
			}()
			if recovered != nil {
				event.Outcome = "panic"
				abort = event.Status != 0 || hijacked || recovered == http.ErrAbortHandler
				if recovered == http.ErrAbortHandler {
					event.Outcome = "aborted"
				}
				if !abort {
					clear(w.Header())
					w.Header().Set("X-Request-ID", event.ID)
					w.Header().Set("Cache-Control", "no-store")
					response.JSON(wrapped, http.StatusInternalServerError, nil, "internal server error")
				}
			} else if r.Context().Err() != nil {
				event.Outcome = "cancelled"
			}
			if abort {
				panic(http.ErrAbortHandler)
			}
		}()
		next.ServeHTTP(wrapped, r)
	})
}

func methodBucket(s string) string {
	switch s {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
		return s
	}
	return "other"
}
