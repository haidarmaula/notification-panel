package middleware

import (
	"fmt"
	"net/http"
)

// Logging is the outermost request-logging middleware for the API server.
//
// Unlike every other middleware in this package, Logging takes and returns
// http.Handler (the interface) rather than http.HandlerFunc. That is because
// it is applied once, at server construction, to wrap the entire
// http.ServeMux:
//
//	server := &http.Server{
//	    Addr:    ":8080",
//	    Handler: middleware.Logging(mux),
//	}
//
// Per-route middleware (API key, JWT, super-admin, audit) continues to use
// the http.HandlerFunc form and is composed separately inside each feature
// module. The two forms are not interchangeable at call sites: Logging
// cannot be passed to Chain, and the HandlerFunc-shaped middleware cannot be
// passed to http.Server.Handler without an explicit adapter.
//
// # What is logged
//
// The middleware writes a single line per request containing the HTTP method
// and the request path, to standard output via fmt.Println. It does not log
// the status code, the response body size, the request duration, the client
// IP, the User-Agent, or any correlation identifier. It also does not
// recover panics — a panic in a downstream handler will propagate out of
// this middleware and be handled by net/http's default panic path, which
// logs to the server's ErrorLog and closes the connection.
//
// # Why this is minimal
//
// This is a placeholder implementation. The project's go.mod already pulls
// in github.com/rs/zerolog, and the README lists "structured logging" as a
// recommended addition; a future revision is expected to replace
// fmt.Println with a structured logger and to add the fields listed above.
// Until then, callers should not rely on this middleware for observability
// beyond a coarse access log.
//
// # Concurrency
//
// The returned handler is safe for concurrent use. The middleware holds no
// per-request state and shares nothing between invocations beyond the
// process-wide standard output, which fmt.Println serialises internally.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		fmt.Println(r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
