//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/auth"
	"hello/internal/features/segments"
	"hello/internal/middleware"
	"hello/internal/token"
)

func TestStaffJWTPolicyAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	q := sqlc.New(tx)
	const actor int64 = 700001
	_, err = tx.Exec(ctx, `INSERT INTO staff_users(id,role_id,name,email,password_hash) SELECT $1,id,'JWT fixture','jwt-policy@fixture.invalid','fixture-only' FROM roles WHERE name='ADMIN'`, actor)
	require.NoError(t, err)
	manager := token.NewTokenManager("fixture-access-key", "fixture-refresh-key")
	api := middleware.NewAPIKeyMiddleware("fixture-api-key")
	jwt := middleware.NewJWTMiddleware(manager, repository.NewStaffUserRepository(q))
	mux := http.NewServeMux()
	auth.NewAuthModule(q, manager, api.Use).RegisterRoutes(mux)
	segments.NewSegmentModule(q, api.Use, jwt.Use).RegisterRoutes(mux)
	request := func(purpose, signed string) *httptest.ResponseRecorder {
		method, path, body := "GET", "/api/v1/segments", ""
		if purpose == "refresh" {
			method = "POST"
			path = "/api/v1/auth/refresh"
			data, err := json.Marshal(map[string]string{"refresh_token": signed})
			require.NoError(t, err)
			body = string(data)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		r.Header.Set("X-API-Key", "fixture-api-key")
		if purpose == "access" {
			r.Header.Set("Authorization", "Bearer "+signed)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, purpose := range []string{"access", "refresh"} {
		for _, variant := range []string{"HS384", "missing exp", "zero identity"} {
			t.Run(purpose+"/"+variant, func(t *testing.T) {
				claims := jwtlib.MapClaims{"user_id": actor, "role_name": "SUPER_ADMIN", "type": purpose, "exp": time.Now().Add(time.Hour).Unix()}
				var method jwtlib.SigningMethod = jwtlib.SigningMethodHS256
				switch variant {
				case "HS384":
					method = jwtlib.SigningMethodHS384
				case "missing exp":
					delete(claims, "exp")
				case "zero identity":
					claims["user_id"] = 0
				}
				secret := "fixture-access-key"
				if purpose == "refresh" {
					secret = "fixture-refresh-key"
				}
				signed, err := jwtlib.NewWithClaims(method, claims).SignedString([]byte(secret))
				require.NoError(t, err)
				w := request(purpose, signed)
				require.Equal(t, 401, w.Code, w.Body.String())
				require.NotContains(t, w.Body.String(), signed)
			})
		}
	}
	access, err := manager.GenerateAccessToken(actor, "VIEWER", "")
	require.NoError(t, err)
	require.Equal(t, 200, request("access", access).Code)
	refresh, err := manager.GenerateRefreshToken(actor, "VIEWER")
	require.NoError(t, err)
	w := request("refresh", refresh)
	require.Equal(t, 200, w.Code, w.Body.String())
	var result struct {
		Data struct {
			AccessToken string `json:"access_token"`
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	claims, err := manager.ParseAccessToken(result.Data.AccessToken)
	require.NoError(t, err)
	require.Equal(t, actor, claims.StaffID)
	require.Equal(t, "ADMIN", claims.RoleName, "refresh must use current database role via staff.RoleID, not token role or staff.ID")
}
