package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"hello/internal/config"
	"hello/internal/database/sqlc"
	"hello/internal/features/applications"
	"hello/internal/provisioning"
)

func TestServerRouteRetirement(t *testing.T) {
	retiredRoutes := []string{
		"DELETE /api/v1/device-tokens/{id}",
		"DELETE /api/v1/notifications/{id}",
		"DELETE /api/v1/segments/{id}",
		"DELETE /api/v1/segments/{id}/members/{userId}",
		"GET /api/v1/notifications",
		"GET /api/v1/notifications/{id}",
		"GET /api/v1/segments",
		"GET /api/v1/segments/{id}",
		"GET /api/v1/segments/{id}/members",
		"GET /api/v1/users",
		"GET /api/v1/users/search",
		"GET /api/v1/users/{id}",
		"GET /api/v1/users/{id}/device-tokens",
		"GET /api/v1/users/{id}/notifications",
		"GET /api/v1/users/{id}/segments",
		"PATCH /api/v1/device-tokens/{id}",
		"PATCH /api/v1/notifications/{id}",
		"PATCH /api/v1/segments/{id}",
		"POST /api/v1/device-tokens",
		"POST /api/v1/mobile/sync",
		"POST /api/v1/notifications",
		"POST /api/v1/notifications/{id}/send",
		"POST /api/v1/segments",
		"POST /api/v1/segments/{id}/members",
	}
	var inventory struct {
		Routes []struct {
			Pattern     string
			Disposition string
		}
	}
	data, err := os.ReadFile("../../docs/contracts/application-scope-inventory.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &inventory))
	pool := new(pgxpool.Pool)
	mux := serverRoutes(sqlc.New(pool), pool, &config.ServerConfig{APIKey: "fixture-key"})
	placeholder := regexp.MustCompile(`\{[^}]+\}`)
	for _, route := range inventory.Routes {
		method, path, ok := strings.Cut(route.Pattern, " ")
		require.True(t, ok, route.Pattern)
		request := httptest.NewRequest(method, placeholder.ReplaceAllString(path, "123"), nil)
		_, pattern := mux.Handler(request)
		if route.Disposition == "configured-mobile" || route.Disposition == "configured-onboarding" {
			require.Empty(t, pattern, route.Pattern)
		} else {
			require.Equal(t, route.Pattern, pattern)
		}
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	service, err := applications.NewOnboardingService(sqlc.New(pool), pool, applications.OnboardingSettings{FingerprintKey: key, FingerprintKeyVersion: 1, Stager: routeOnlyStager{}})
	require.NoError(t, err)
	enabled := serverRoutes(sqlc.New(pool), pool, &config.ServerConfig{APIKey: "fixture-key"}, service)
	for _, route := range inventory.Routes {
		if route.Disposition != "configured-onboarding" {
			continue
		}
		method, path, ok := strings.Cut(route.Pattern, " ")
		require.True(t, ok)
		request := httptest.NewRequest(method, placeholder.ReplaceAllString(path, "123"), nil)
		_, pattern := enabled.Handler(request)
		require.Equal(t, route.Pattern, pattern)
		denied := httptest.NewRecorder()
		enabled.ServeHTTP(denied, request)
		require.Equal(t, 401, denied.Code, route.Pattern)
		require.Equal(t, "no-store", denied.Header().Get("Cache-Control"), route.Pattern)
	}

	for _, route := range retiredRoutes {
		method, path, ok := strings.Cut(route, " ")
		require.True(t, ok, route)
		request := httptest.NewRequest(method, placeholder.ReplaceAllString(path, "123"), nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		require.Equal(t, 404, response.Code, route)
	}
}

// Route matching/auth rejection must never publish a credential or reach a DB.
type routeOnlyStager struct{}

func (routeOnlyStager) Stage(context.Context, provisioning.StageRequest) (provisioning.StageResult, error) {
	panic("route test reached credential publication")
}
