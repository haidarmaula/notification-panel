package middleware

import (
	"context"
	"net/http"
	"strings"
)

type auditContextKey string

const (
	AuditIPKey        auditContextKey = "audit_ip"
	AuditUserAgentKey auditContextKey = "audit_user_agent"
)

func AuditMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.Header.Get("X-Forwarder-For")
		if ip == "" {
			ip = r.RemoteAddr
		}
		if idx := strings.Index(ip, ","); idx != -1 {
			ip = strings.TrimSpace(ip[:idx])
		}
		ctx := context.WithValue(r.Context(), AuditIPKey, ip)
		ctx = context.WithValue(ctx, AuditUserAgentKey, r.UserAgent())
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}
