package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"hello/internal/config"
	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// Every documented staff path must reach its real guard without a live database.
// Malformed login/refresh/logout bodies may reach the handler only after the API key.
func TestStaffOpenAPIRegistration(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]struct {
			Responses map[string]json.RawMessage
		}
	}
	b, err := os.ReadFile("../../docs/contracts/staff-business.openapi.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &spec))
	require.NotEmpty(t, spec.Paths)
	pool := new(pgxpool.Pool)
	mux := serverRoutes(sqlc.New(pool), pool, &config.ServerConfig{APIKey: "fixture-contract-key"})
	param := regexp.MustCompile(`\{[^}]+\}`)
	for path, methods := range spec.Paths {
		for method, operation := range methods {
			t.Run(method+" "+path, func(t *testing.T) {
				require.Contains(t, operation.Responses, "401")
				send := func(key bool) (int, map[string]any) {
					r := httptest.NewRequest(strings.ToUpper(method), param.ReplaceAllString(path, "123"), strings.NewReader("{"))
					r.Header.Set("Content-Type", "application/json")
					if key {
						r.Header.Set("X-API-Key", "fixture-contract-key")
					}
					_, pattern := mux.Handler(r)
					require.Equal(t, strings.ToUpper(method)+" "+path, pattern)
					w := httptest.NewRecorder()
					mux.ServeHTTP(w, r)
					var body map[string]any
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
					require.Equal(t, false, body["success"])
					require.NotContains(t, body, "data")
					return w.Code, body
				}
				status, _ := send(false)
				require.Equal(t, http.StatusUnauthorized, status)
				status, body := send(true)
				if strings.HasPrefix(path, "/api/v1/auth/") {
					require.Contains(t, operation.Responses, "400")
					require.Equal(t, http.StatusBadRequest, status)
					require.Equal(t, "invalid request body", body["message"])
				} else {
					require.Equal(t, http.StatusUnauthorized, status)
					require.Equal(t, "missing token", body["message"])
				}
			})
		}
	}
}
