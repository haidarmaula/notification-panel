package profile

import (
	"context"

	"hello/internal/middleware"
	"hello/internal/token"
)

// GetStaffIDFromContext retrieves the staff ID from the JWT claims stored in context.
func GetStaffIDFromContext(ctx context.Context) (int64, bool) {
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	if !ok || claims == nil {
		return 0, false
	}
	return claims.StaffID, true
}
