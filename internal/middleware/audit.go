package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

type auditContextKey string

const (
	AuditIPKey        auditContextKey = "audit_ip"
	AuditUserAgentKey auditContextKey = "audit_user_agent"
)

type AuditMiddleware struct {
	IPHeader string
}

func NewAuditMiddleware() *AuditMiddleware {
	return &AuditMiddleware{
		IPHeader: "X-Forwarded-For",
	}
}

func (m *AuditMiddleware) Use(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractClientIP(r, m.IPHeader)
		ctx := context.WithValue(r.Context(), AuditIPKey, ip)
		ctx = context.WithValue(ctx, AuditUserAgentKey, r.UserAgent())
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

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
