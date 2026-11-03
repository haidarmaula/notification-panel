//go:build integration

package integration

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"hello/internal/config"
	"hello/internal/database"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/auth"
	"hello/internal/features/mobile"
	"hello/internal/features/notifications"
	"hello/internal/features/profile"
	"hello/internal/features/segments"
	"hello/internal/features/staff"
	"hello/internal/features/users"
	"hello/internal/httprequest"
	"hello/internal/middleware"
	"hello/internal/token"
)

func TestJSONRequestLimitsAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := database.New(ctx, databaseURL)
	require.NoError(t, err)
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	q := sqlc.New(tx)
	var actor int64
	require.NoError(t, tx.QueryRow(ctx, `INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Body fixture',gen_random_uuid()::text || '@fixture.invalid','fixture-only' FROM roles WHERE name='SUPER_ADMIN' RETURNING id`).Scan(&actor))
	tokens := token.NewTokenManager("fixture-access-not-production", "fixture-refresh-not-production")
	access, err := tokens.GenerateAccessToken(actor, "VIEWER", "")
	require.NoError(t, err)
	mobileAccess, err := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, jwtlib.MapClaims{"external_id": "body-fixture", "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("fixture-mobile-secret"))
	require.NoError(t, err)
	api := middleware.NewAPIKeyMiddleware("fixture-api-key")
	jwt := middleware.NewJWTMiddleware(tokens, repository.NewStaffUserRepository(q))
	mobileJWT := middleware.NewMobileJWTMiddleware("fixture-mobile-secret")
	auditMW := middleware.NewAuditMiddleware()
	cfg := &config.ServerConfig{MobileJWTSecret: "fixture-mobile-secret", KafkaBroker: "127.0.0.1:19092", SendTopic: "notification.send.requested"}
	mux := http.NewServeMux()
	auth.NewAuthModule(q, tokens, api.Use).RegisterRoutes(mux)
	// Keep the current server wiring: mobile/sync has no outer middleware.
	mobile.NewMobileModule(q, cfg).RegisterRoutes(mux)
	profile.NewProfileModule(q, api.Use, jwt.Use).RegisterRoutes(mux)
	staff.NewStaffModule(q, api.Use, jwt.Use, auditMW.Use).RegisterRoutes(mux)
	segments.NewSegmentModule(q, api.Use, jwt.Use, auditMW.Use).RegisterRoutes(mux)
	notifications.NewNotificationModule(q, cfg, api.Use, jwt.Use, auditMW.Use).RegisterRoutes(mux)
	users.NewUserModule(q, pool, []middleware.Middleware{api.Use, jwt.Use}, []middleware.Middleware{mobileJWT.Use}).RegisterRoutes(mux)
	snapshot := func() string {
		var data string
		require.NoError(t, tx.QueryRow(ctx, `SELECT jsonb_build_array(
  (SELECT jsonb_agg(x ORDER BY id) FROM users x),
  (SELECT jsonb_agg(x ORDER BY id) FROM device_tokens x),
  (SELECT jsonb_agg(x ORDER BY id) FROM staff_users x),
  (SELECT jsonb_agg(x ORDER BY id) FROM notifications x),
  (SELECT jsonb_agg(x ORDER BY id) FROM notification_targets x),
  (SELECT jsonb_agg(x ORDER BY id) FROM segments x),
  (SELECT jsonb_agg(x ORDER BY id) FROM segment_members x),
  (SELECT jsonb_agg(x ORDER BY id) FROM audit_logs x),
  (SELECT jsonb_agg(x ORDER BY id) FROM notification_deliveries x),
  (SELECT jsonb_agg(x ORDER BY id) FROM notification_reads x))::text`).Scan(&data))
		return data
	}
	request := func(method, path, body, access, omit string, unknown bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		r.Header.Set("X-API-Key", "fixture-api-key")
		r.Header.Set("Authorization", "Bearer "+access)
		// Missing Content-Type remains supported for compatibility.
		if omit != "" {
			r.Header.Del(omit)
		}
		if unknown {
			r.ContentLength = -1
			r.TransferEncoding = []string{"chunked"}
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	offsets := []int64{lastTopicOffset(t, ctx, "notification.send.requested"), lastTopicOffset(t, ctx, "notification.delivery.updated")}
	for _, route := range []struct {
		method, path string
		limit        int64
		mobile       bool
	}{
		{"POST", "/api/v1/auth/login", httprequest.DefaultJSONLimit, false},
		{"POST", "/api/v1/auth/refresh", httprequest.DefaultJSONLimit, false},
		{"POST", "/api/v1/mobile/sync", httprequest.DefaultJSONLimit, false},
		{"PATCH", "/api/v1/profile", httprequest.DefaultJSONLimit, false},
		{"PATCH", "/api/v1/profile/password", httprequest.DefaultJSONLimit, false},
		{"POST", "/api/v1/staff", httprequest.DefaultJSONLimit, false},
		{"PATCH", "/api/v1/staff/1", httprequest.DefaultJSONLimit, false},
		{"PATCH", "/api/v1/staff/1/status", httprequest.DefaultJSONLimit, false},
		{"PATCH", "/api/v1/staff/1/password", httprequest.DefaultJSONLimit, false},
		{"POST", "/api/v1/segments", httprequest.DefaultJSONLimit, false},
		{"PATCH", "/api/v1/segments/1", httprequest.DefaultJSONLimit, false},
		{"POST", "/api/v1/segments/1/members", httprequest.DefaultJSONLimit, false},
		{"POST", "/api/v1/device-tokens", httprequest.DefaultJSONLimit, true},
		{"PATCH", "/api/v1/device-tokens/1", httprequest.DefaultJSONLimit, true},
		{"POST", "/api/v1/notifications", httprequest.CampaignJSONLimit, false},
		{"PATCH", "/api/v1/notifications/1", httprequest.CampaignJSONLimit, false},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			token := access
			if route.mobile {
				token = mobileAccess
			}
			for _, variant := range []struct {
				name, body string
				unknown    bool
				want       int
			}{
				{"known oversized", `{}` + strings.Repeat(" ", int(route.limit)), false, 413},
				{"unknown oversized", `{}` + strings.Repeat(" ", int(route.limit)), true, 413},
				{"multiple JSON values", `{} {}`, true, 400},
			} {
				t.Run(variant.name, func(t *testing.T) {
					before := snapshot()
					w := request(route.method, route.path, variant.body, token, "", variant.unknown)
					require.Equal(t, variant.want, w.Code, w.Body.String())
					require.Equal(t, before, snapshot(), "rejected body mutated business/audit data")
					message := "request body too large"
					if variant.want == 400 {
						message = "invalid request body"
					}
					require.Contains(t, w.Body.String(), message)
				})
			}
		})
	}
	t.Run("actual HTTP chunked body", func(t *testing.T) {
		seen := make(chan bool, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen <- r.ContentLength == -1 && len(r.TransferEncoding) == 1 && r.TransferEncoding[0] == "chunked"
			mux.ServeHTTP(w, r)
		}))
		defer server.Close()
		client := server.Client()
		client.Timeout = 5 * time.Second
		defer client.CloseIdleConnections()
		body := io.NopCloser(strings.NewReader(`{}` + strings.Repeat(" ", int(httprequest.DefaultJSONLimit))))
		r, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/v1/auth/login", body)
		require.NoError(t, err)
		r.Header.Set("X-API-Key", "fixture-api-key")
		before := snapshot()
		result, err := client.Do(r)
		require.NoError(t, err)
		defer result.Body.Close()
		data, err := io.ReadAll(result.Body)
		require.NoError(t, err)
		require.True(t, <-seen, "client must actually transmit HTTP/1 chunked framing")
		require.Equal(t, 413, result.StatusCode)
		require.JSONEq(t, `{"success":false,"message":"request body too large"}`, string(data))
		require.Equal(t, before, snapshot())
	})

	t.Run("existing gates run first", func(t *testing.T) {
		oversized := strings.Repeat("x", int(httprequest.CampaignJSONLimit)+1)
		before := snapshot()
		w := request("POST", "/api/v1/auth/login", oversized, access, "X-API-Key", true)
		require.Equal(t, 401, w.Code)
		w = request("POST", "/api/v1/staff", oversized, access, "Authorization", true)
		require.Equal(t, 401, w.Code)
		_, err = tx.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER') WHERE id=$1", actor)
		require.NoError(t, err)
		demotedBefore := snapshot()
		w = request("POST", "/api/v1/notifications", oversized, access, "", true)
		require.Equal(t, 403, w.Code)
		require.Equal(t, demotedBefore, snapshot())
		_, err = tx.Exec(ctx, "UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='SUPER_ADMIN') WHERE id=$1", actor)
		require.NoError(t, err)
		require.Equal(t, before, snapshot())
	})
	for _, route := range []struct {
		path, body string
		limit      int64
	}{
		{"/api/v1/segments", `{"name":"Exact limit fixture"}`, httprequest.DefaultJSONLimit},
		{"/api/v1/notifications", `{"title":"Exact limit fixture","body":"Fixture"}`, httprequest.CampaignJSONLimit},
	} {
		t.Run("exact limit "+route.path, func(t *testing.T) {
			body := route.body + strings.Repeat(" ", int(route.limit)-len(route.body))
			w := request("POST", route.path, body, access, "", true)
			require.Equal(t, 201, w.Code, w.Body.String())
		})
	}
	require.Equal(t, offsets, []int64{lastTopicOffset(t, ctx, "notification.send.requested"), lastTopicOffset(t, ctx, "notification.delivery.updated")}, "body rejections/exact-limit drafts must not publish Kafka events")
}
