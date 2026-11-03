package token_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"hello/internal/features/auth"
	"hello/internal/middleware"
	"hello/internal/token"
)

type statusProbe struct{ calls int }

func (p *statusProbe) IsActive(context.Context, int64) (bool, error) { p.calls++; return true, nil }

func TestStaffHTTPRejectsBeforeLookup(t *testing.T) {
	for _, purpose := range []string{"access", "refresh"} {
		for _, variant := range []string{"HS384", "missing exp", "zero identity"} {
			t.Run(purpose+"/"+variant, func(t *testing.T) {
				claims := jwt.MapClaims{"type": purpose, "user_id": 7, "exp": time.Now().Add(time.Hour).Unix()}
				var method jwt.SigningMethod = jwt.SigningMethodHS256
				switch variant {
				case "HS384":
					method = jwt.SigningMethodHS384
				case "missing exp":
					delete(claims, "exp")
				case "zero identity":
					claims["user_id"] = 0
				}
				secret := "fixture-access-key"
				if purpose == "refresh" {
					secret = "fixture-refresh-key"
				}
				signed, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
				require.NoError(t, err)
				manager := token.NewTokenManager("fixture-access-key", "fixture-refresh-key")
				w := httptest.NewRecorder()
				if purpose == "access" {
					probe := &statusProbe{}
					guard := middleware.NewJWTMiddleware(manager, probe)
					r := httptest.NewRequest("GET", "/", nil)
					r.Header.Set("Authorization", "Bearer "+signed)
					guard.Use(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid access reached handler") })(w, r)
					require.Zero(t, probe.calls)
				} else {
					// Actual auth route/service, nil database queries: a lookup would panic.
					mux := http.NewServeMux()
					api := middleware.NewAPIKeyMiddleware("fixture-api-key")
					auth.NewAuthModule(nil, manager, api.Use).RegisterRoutes(mux)
					body, err := json.Marshal(map[string]string{"refresh_token": signed})
					require.NoError(t, err)
					r := httptest.NewRequest("POST", "/api/v1/auth/refresh", strings.NewReader(string(body)))
					r.Header.Set("X-API-Key", "fixture-api-key")
					mux.ServeHTTP(w, r)
				}
				require.Equal(t, 401, w.Code)
				require.NotContains(t, w.Body.String(), signed)
				require.NotContains(t, w.Body.String(), "fixture-")
			})
		}
	}
}
