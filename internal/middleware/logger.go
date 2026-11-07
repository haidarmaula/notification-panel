package middleware

import (
	"log/slog"
	"net/http"

	"hello/internal/observability"
)

// Logging records safe route templates and committed responses for standalone
// handlers. The daemon wires the same access wrapper to its private registry.
func Logging(next http.Handler) http.Handler {
	return observability.Access(next, func(r observability.Request) {
		slog.Info("http_request", "request_id", r.ID, "method", r.Method, "route", r.Route, "status", r.Status, "bytes", r.Bytes, "duration_seconds", r.Duration.Seconds(), "outcome", r.Outcome)
	})
}
