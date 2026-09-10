package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// auditContextKey is the private type used for audit-related context keys.
//
// It exists to prevent collisions with other context-key namespaces. Go's
// context package deliberately treats keys as opaque; two keys with the same
// underlying string value but different static types are still distinct, so
// declaring a dedicated type here (as opposed to reusing a shared
// contextKey type) ensures that an audit key can never accidentally match a
// key from another middleware package. Do not merge this type with the
// contextKey type declared in jwt.go — the separation is the mechanism.
type auditContextKey string

// AuditIPKey is the context key under which the resolved client IP is stored.
//
// The value is a string set by AuditMiddleware.Use and read by the audit
// package's GetIP helper. It is exported because the audit package, which
// lives in a different import path, needs to reference it; callers outside
// of the audit subsystem should retrieve the value through that helper
// rather than reading the context directly.
const AuditIPKey auditContextKey = "audit_ip"

// AuditUserAgentKey is the context key under which the raw User-Agent header
// is stored.
//
// The value is a string set by AuditMiddleware.Use and read by the audit
// package's GetUserAgent helper. As with AuditIPKey, direct reads are
// discouraged outside the audit package.
const AuditUserAgentKey auditContextKey = "audit_user_agent"

// AuditMiddleware records request-scoped attributes needed by the audit
// subsystem: the client IP address and the raw User-Agent header.
//
// It performs no logging and makes no authorization decisions. Its sole
// purpose is to extract these two attributes from the incoming request and
// attach them to the request context so that downstream handlers — and
// ultimately the audit service — can read them without re-parsing the
// request. Placing this work in middleware keeps the extraction logic (in
// particular, the X-Forwarded-For handling) in one place instead of being
// duplicated in every audit call site.
//
// The middleware is stateless after construction apart from its IPHeader
// configuration field and is safe for concurrent use.
type AuditMiddleware struct {
	// IPHeader is the name of the HTTP header from which the client IP is
	// read. It is exported so that deployments with a non-standard proxy
	// configuration can override the default without editing this package.
	// An empty value causes the middleware to skip header-based lookup and
	// fall back to RemoteAddr.
	IPHeader string
}

// NewAuditMiddleware constructs an AuditMiddleware configured to read the
// client IP from the X-Forwarded-For header.
//
// X-Forwarded-For is the correct default for a deployment behind a
// standards-compliant reverse proxy (nginx, Envoy, an ALB). It is NOT safe
// if the service is exposed directly to the internet, because the header is
// client-controlled in that case — see extractClientIP for the trust
// assumptions this implies. Deployments that cannot guarantee a trusted
// proxy should override IPHeader to a value the edge itself sets, or accept
// that the recorded IP is advisory only.
func NewAuditMiddleware() *AuditMiddleware {
	return &AuditMiddleware{
		IPHeader: "X-Forwarded-For",
	}
}

// Use wraps next with audit-context injection.
//
// On every request the middleware resolves the client IP from the configured
// header (or falls back to RemoteAddr), reads the raw User-Agent string, and
// stores both on a copy of the request context. The augmented context is
// propagated to next via r.WithContext; the original request is not mutated.
//
// # Failure behaviour
//
// The middleware never rejects a request. If the IP cannot be determined,
// an empty string is recorded; if the User-Agent header is absent, an empty
// string is recorded. Downstream audit entries with empty values are
// legitimate — for example, when an action is triggered by an internal job
// rather than an HTTP request — and are not treated as errors.
//
// # Concurrency
//
// The returned handler is safe for concurrent use. It does not mutate the
// receiver and does not share mutable state across requests.
func (m *AuditMiddleware) Use(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractClientIP(r, m.IPHeader)
		ctx := context.WithValue(r.Context(), AuditIPKey, ip)
		ctx = context.WithValue(ctx, AuditUserAgentKey, r.UserAgent())
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

// extractClientIP resolves the client's IP address from the request,
// preferring the value in the named header and falling back to RemoteAddr.
//
// # Header handling
//
// When the configured header is present, its value is assumed to be a
// standard X-Forwarded-For list — one or more comma-separated addresses,
// typically of the form "client, proxy1, proxy2". The leftmost entry, which
// is the original client according to the X-Forwarded-For convention, is
// returned after trimming surrounding whitespace. Only the first comma is
// considered; IPv6 addresses, which may contain colons but not commas, are
// returned intact.
//
// # Trust model
//
// This function trusts the header unconditionally. That is safe only when
// the deployment terminates at a reverse proxy that strips any inbound
// X-Forwarded-For and sets its own. If the service is reachable directly,
// an attacker can send an arbitrary X-Forwarded-For header and the recorded
// IP will be whatever they chose. Audit entries should therefore be treated
// as operationally useful but not as authoritative for security decisions.
//
// # Fallback
//
// When the header is absent, the function falls back to r.RemoteAddr, which
// for a Go HTTP server is a "host:port" string. The port is stripped with
// net.SplitHostPort; if parsing fails — which can happen for unusual
// listeners — the raw RemoteAddr is returned unmodified rather than
// discarded, on the assumption that a partially-correct value is more
// useful for debugging than an empty one.
func extractClientIP(r *http.Request, header string) string {
	if fwd := r.Header.Get(header); fwd != "" {
		// X-Forwarded-For can be a comma-separated list; leftmost is the original client
		if idx := strings.Index(fwd, ","); idx != -1 {
			fwd = fwd[:idx]
		}
		return strings.TrimSpace(fwd)
	}

	// Fallback: RemoteAddr is host:port, strip the port
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// couldn't parse, return as-is
		return r.RemoteAddr
	}
	return host
}
