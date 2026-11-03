package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"hello/internal/config"
	"hello/internal/database/sqlc"
)

func TestServerRouteRetirement(t *testing.T) {
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
	retired := 0
	for _, route := range inventory.Routes {
		method, path, ok := strings.Cut(route.Pattern, " ")
		require.True(t, ok, route.Pattern)
		request := httptest.NewRequest(method, placeholder.ReplaceAllString(path, "123"), nil)
		_, pattern := mux.Handler(request)
		if route.Disposition == "retired-unmounted" {
			retired++
			require.Empty(t, pattern, route.Pattern)
		} else if route.Disposition == "configured-mobile" {
			require.Empty(t, pattern, route.Pattern)
		} else {
			require.Equal(t, route.Pattern, pattern)
		}
	}
	require.Equal(t, 24, retired)
}
