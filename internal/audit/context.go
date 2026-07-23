package audit

import (
	"context"
	"hello/internal/middleware"
	"hello/internal/token"
)

// GetStaffIDFromContext retrieves staff ID from context.
func GetStaffID(ctx context.Context) (int64, bool) {
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	if !ok || claims == nil {
		return 0, false
	}
	return claims.StaffID, true
}

func GetIP(ctx context.Context) string {
	ip, _ := ctx.Value(middleware.AuditIPKey).(string)
	return ip
}

func GetUserAgent(ctx context.Context) string {
	ua, _ := ctx.Value(middleware.AuditUserAgentKey).(string)
	return ua
}

func GetAuditContext(ctx context.Context) (actorID int64, ip string, userAgent string, ok bool) {
	actorID, ok = GetStaffID(ctx)
	if !ok {
		return 0, "", "", false
	}
	ip = GetIP(ctx)
	userAgent = GetUserAgent(ctx)
	return actorID, ip, userAgent, true
}
