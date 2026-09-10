package audit

import (
	"context"
	"hello/internal/middleware"
	"hello/internal/token"
)

// GetStaffID extracts the authenticated staff user's ID from ctx.
//
// The ID is placed into the context by the JWT authentication middleware as
// part of *token.AccessClaims under middleware.UserContextKey. This helper is
// the supported way for downstream code (such as AuditService.Log) to recover
// the actor identity without importing the token package directly.
//
// The second return value reports whether a valid, non-nil claims value was
// present. Callers that require a well-formed actor must check it; callers
// that treat auditing as best-effort (as AuditService.Log currently does) may
// ignore it and accept the zero ID.
//
// The returned ID is the internal numeric staff_users.id, not any external
// identifier.
func GetStaffID(ctx context.Context) (int64, bool) {
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	if !ok || claims == nil {
		return 0, false
	}
	return claims.StaffID, true
}

// GetIP returns the client IP address recorded in ctx by the audit middleware.
//
// The value is stored under middleware.AuditIPKey. If no value is present —
// for example, because the call originates from a background worker rather
// than an HTTP request — an empty string is returned rather than an error;
// audit entries with an empty IP are legitimate for system-initiated actions.
func GetIP(ctx context.Context) string {
	ip, _ := ctx.Value(middleware.AuditIPKey).(string)
	return ip
}

// GetUserAgent returns the client User-Agent header recorded in ctx by the
// audit middleware.
//
// The value is stored under middleware.AuditUserAgentKey. As with GetIP, an
// empty string is returned when the header is absent or the context did not
// originate from an HTTP request, and this is not treated as an error.
func GetUserAgent(ctx context.Context) string {
	ua, _ := ctx.Value(middleware.AuditUserAgentKey).(string)
	return ua
}

// GetAuditContext is a convenience helper that atomically extracts the three
// actor attributes an audit entry needs: the acting staff ID, the client IP,
// and the client User-Agent.
//
// The fourth return value (ok) reports whether a valid staff identity was
// found. When ok is false, the other three values are the zero values of
// their types — (0, "", "", false) — and the caller must decide whether to
// reject the audit entry or record it as a system/anonymous action. The IP
// and User-Agent are always returned as-is (possibly empty) when ok is true;
// their absence is not considered a failure.
//
// Use this function when you need more than one of the three attributes and
// want a single, consistent extraction point. For retrieving just the actor
// ID, prefer GetStaffID, which also reports presence via a bool.
func GetAuditContext(ctx context.Context) (actorID int64, ip string, userAgent string, ok bool) {
	actorID, ok = GetStaffID(ctx)
	if !ok {
		return 0, "", "", false
	}
	ip = GetIP(ctx)
	userAgent = GetUserAgent(ctx)
	return actorID, ip, userAgent, true
}
