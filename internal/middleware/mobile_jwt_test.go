package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestMobileJWTMiddlewareUse(t *testing.T) {
	const secret = "mobile-test-secret"
	middleware := NewMobileJWTMiddleware(secret)

	validToken := signedMobileToken(t, secret, jwt.SigningMethodHS256, "user-123")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/device-tokens", nil)
	request.Header.Set("Authorization", "Bearer "+validToken)
	recorder := httptest.NewRecorder()

	middleware.Use(func(w http.ResponseWriter, r *http.Request) {
		externalID, ok := GetMobileExternalID(r.Context())
		if !ok || externalID != "user-123" {
			t.Fatalf("mobile identity = %q, %t; want user-123, true", externalID, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestMobileJWTMiddlewareUseRejectsInvalidIdentity(t *testing.T) {
	const secret = "mobile-test-secret"
	middleware := NewMobileJWTMiddleware(secret)

	tests := []struct {
		name  string
		token string
	}{
		{name: "missing external ID", token: signedMobileToken(t, secret, jwt.SigningMethodHS256, "")},
		{name: "wrong algorithm", token: signedMobileToken(t, secret, jwt.SigningMethodHS384, "user-123")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodDelete, "/api/v1/device-tokens/1", nil)
			request.Header.Set("Authorization", "Bearer "+test.token)
			recorder := httptest.NewRecorder()

			middleware.Use(func(http.ResponseWriter, *http.Request) {
				t.Fatal("next handler must not run")
			}).ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
		})
	}
}

func signedMobileToken(t *testing.T, secret string, method jwt.SigningMethod, externalID string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"external_id": externalID,
		"exp":         time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(method, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}
