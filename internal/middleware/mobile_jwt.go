package middleware

import (
	"context"
	"net/http"
	"strings"

	"hello/pkg/response"

	"github.com/golang-jwt/jwt/v5"
)

// MobileExternalIDContextKey is the context key under which MobileJWTMiddleware
// stores the authenticated mobile user's external identifier.
//
// The value is a non-empty string sourced from the verified Laravel JWT's
// external_id claim. Handlers should retrieve it with GetMobileExternalID
// rather than asserting the context value directly. The key is absent unless
// MobileJWTMiddleware has accepted the request.
const MobileExternalIDContextKey = contextKey("mobile_external_id")

// MobileJWTMiddleware verifies Laravel-issued bearer tokens for mobile-user
// endpoints and exposes the authenticated external user ID in request context.
//
// The middleware accepts only HS256 JWTs signed with secret. It requires a
// non-empty string external_id claim because that is the cross-system identity
// used to resolve users in this service. It is stateless after construction and
// safe for concurrent use.
type MobileJWTMiddleware struct {
	secret []byte
}

// NewMobileJWTMiddleware constructs a MobileJWTMiddleware using secret.
//
// secret must be the shared MOBILE_JWT_SECRET used by Laravel. An empty secret
// is not rejected here; configuration loading is responsible for rejecting it
// before the server starts. The returned middleware owns an immutable byte copy
// of the string and requires no cleanup.
func NewMobileJWTMiddleware(secret string) *MobileJWTMiddleware {
	return &MobileJWTMiddleware{secret: []byte(secret)}
}

// GetMobileExternalID returns the verified mobile external ID stored in ctx.
//
// It returns "", false when ctx did not pass through MobileJWTMiddleware or
// when the value has an unexpected type. Callers must reject that case rather
// than trusting an identity supplied in a request body or path.
func GetMobileExternalID(ctx context.Context) (string, bool) {
	externalID, ok := ctx.Value(MobileExternalIDContextKey).(string)
	return externalID, ok && externalID != ""
}

// Use wraps next with Laravel mobile-JWT authentication.
//
// The request must carry an Authorization header in the form "Bearer <JWT>".
// Invalid, expired, incorrectly signed, non-HS256, or claim-incomplete tokens
// receive 401 and never reach next. On success the verified external_id is
// attached to a derived request context. The returned handler is safe for
// concurrent use.
func (m *MobileJWTMiddleware) Use(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		parts := strings.Fields(authHeader)
		if len(parts) != 2 || parts[0] != "Bearer" {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid mobile token")
			return
		}

		claims := jwt.MapClaims{}
		parsed, err := jwt.ParseWithClaims(parts[1], claims, func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, jwt.ErrSignatureInvalid
			}
			return m.secret, nil
		})
		if err != nil || parsed == nil || !parsed.Valid {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid mobile token")
			return
		}

		externalID, ok := claims["external_id"].(string)
		if !ok || externalID == "" {
			response.JSON(w, http.StatusUnauthorized, nil, "invalid mobile token")
			return
		}

		ctx := context.WithValue(r.Context(), MobileExternalIDContextKey, externalID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
