//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/features/auth"
	"hello/internal/middleware"
	"hello/internal/token"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestStaffSessionFamilies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := acceptanceFixture(t, ctx)
	hash, err := bcrypt.GenerateFromPassword([]byte("fixture-login"), bcrypt.MinCost)
	require.NoError(t, err)
	var staffID int64
	require.NoError(t, pool.QueryRow(ctx, "INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Fixture','sessions@fixture.test',$1 FROM roles WHERE name='ADMIN' RETURNING id", string(hash)).Scan(&staffID))
	q := sqlc.New(pool)
	tokens := protectedSessionTokens(t, 1, true)
	api := middleware.NewAPIKeyMiddleware("fixture-api-key")
	routes := func(tokens *token.TokenManager) *http.ServeMux {
		mux := http.NewServeMux()
		auth.NewAuthModule(q, tokens, api.Use).RegisterRoutes(mux)
		guard := middleware.NewJWTMiddleware(tokens, repository.NewStaffUserRepository(q))
		mux.HandleFunc("GET /current", guard.Use(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
		return mux
	}
	mux := routes(tokens)
	do := func(method, path string, body []byte, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(ctx)
		req.Header.Set("X-API-Key", "fixture-api-key")
		req.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	decode := func(w *httptest.ResponseRecorder) auth.LoginResponse {
		t.Helper()
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		var envelope struct {
			Data auth.LoginResponse `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		require.NotEmpty(t, envelope.Data.RefreshToken)
		require.NotEmpty(t, envelope.Data.AccessToken)
		return envelope.Data
	}
	login := func() auth.LoginResponse {
		return decode(do("POST", "/api/v1/auth/login", []byte(`{"email":"sessions@fixture.test","password":"fixture-login"}`), ""))
	}
	renew := func(path, refresh string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(auth.RefreshTokenRequest{RefreshToken: refresh})
		return do("POST", "/api/v1/auth/"+path, b, "")
	}
	current := func(p auth.LoginResponse, want int) {
		t.Helper()
		require.Equal(t, want, do("GET", "/current", nil, p.AccessToken).Code)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, query, args...)
		require.NoError(t, err)
	}
	first, second, oldSignerFamily := login(), login(), login()
	current(first, 204)
	// A rolling restart changes the issuer's active keys without modifying
	// credentials already presented or any database session.
	tokens = protectedSessionTokens(t, 2, true)
	mux = routes(tokens)
	current(first, 204)
	next := decode(renew("refresh", first.RefreshToken))
	require.NotEqual(t, first.RefreshToken, next.RefreshToken)
	oldClaims, err := tokens.ParseRefreshToken(first.RefreshToken)
	require.NoError(t, err)
	newClaims, err := tokens.ParseRefreshToken(next.RefreshToken)
	require.NoError(t, err)
	require.Equal(t, oldClaims.SessionID, newClaims.SessionID)
	require.Equal(t, oldClaims.ExpiresAt.Time, newClaims.ExpiresAt.Time)
	require.NotEqual(t, oldClaims.ID, newClaims.ID)
	var digestLen int
	require.NoError(t, pool.QueryRow(ctx, "SELECT octet_length(refresh_digest) FROM staff_sessions WHERE id=$1::uuid", oldClaims.SessionID).Scan(&digestLen))
	require.Equal(t, 32, digestLen)
	current(next, 204)
	require.Equal(t, 401, renew("refresh", first.RefreshToken).Code)
	current(next, 401)
	require.Equal(t, 401, renew("refresh", next.RefreshToken).Code)
	current(second, 204)
	require.Equal(t, 200, renew("logout", second.RefreshToken).Code)
	require.Equal(t, 200, renew("logout", second.RefreshToken).Code)
	current(second, 401)
	require.Equal(t, 401, renew("refresh", second.RefreshToken).Code)

	activeSignerFamily := login()
	tokens = protectedSessionTokens(t, 2, false)
	mux = routes(tokens)
	current(oldSignerFamily, 401)
	require.Equal(t, 401, renew("refresh", oldSignerFamily.RefreshToken).Code)
	current(activeSignerFamily, 204)
	current(decode(renew("refresh", activeSignerFamily.RefreshToken)), 204)

	concurrent := login()
	results := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { <-start; results <- renew("refresh", concurrent.RefreshToken) })
	}
	close(start)
	wg.Wait()
	close(results)
	successes, failures := 0, 0
	for result := range results {
		switch result.Code {
		case 200:
			successes++
			current(decode(result), 401)
		case 401:
			failures++
		default:
			t.Fatalf("unexpected rotation status %d", result.Code)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, failures)
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM staff_sessions WHERE generation=2 AND revocation_reason='refresh_reuse'").Scan(&count))
	require.Equal(t, 2, count)

	for _, change := range []func(){
		func() { exec("UPDATE staff_users SET is_active=false; UPDATE staff_users SET is_active=true") },
		func() {
			exec("UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='VIEWER'); UPDATE staff_users SET role_id=(SELECT id FROM roles WHERE name='ADMIN')")
		},
		func() {
			exec("UPDATE staff_users SET password_hash='temporary'")
			exec("UPDATE staff_users SET password_hash=$1 WHERE id=$2", string(hash), staffID)
		},
		func() { exec("UPDATE staff_session_control SET session_epoch=gen_random_uuid()") },
	} {
		p := login()
		change()
		current(p, 401)
		require.Equal(t, 401, renew("refresh", p.RefreshToken).Code)
	}
	p := login()
	exec("UPDATE staff_sessions SET expires_at=created_at+interval '1 microsecond' WHERE revoked_at IS NULL")
	current(p, 401)
	require.Equal(t, 401, renew("refresh", p.RefreshToken).Code)
}

// protectedSessionTokens constructs an independent replica using synthetic,
// disjoint keys for both token purposes and the requested rollout generation.
func protectedSessionTokens(t *testing.T, active int, keepOld bool) *token.TokenManager {
	t.Helper()
	ring := func(prefix string, first byte) map[string]any {
		var keys []map[string]string
		for generation := 1; generation <= 2; generation++ {
			if generation == 1 && !keepOld {
				continue
			}
			id := prefix + strconv.Itoa(generation)
			key := map[string]string{"kid": id, "key": base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{first + byte(generation)}, 32))}
			if generation != active {
				key["verify_until"] = time.Now().UTC().Add(25 * time.Hour).Format(time.RFC3339)
			}
			keys = append(keys, key)
		}
		return map[string]any{"active_kid": prefix + strconv.Itoa(active), "audience": "urn:fixture:" + prefix, "keys": keys}
	}
	raw, err := json.Marshal(map[string]any{"issuer": "urn:fixture:staff", "access": ring("access", 1), "refresh": ring("refresh", 3)})
	require.NoError(t, err)
	manager, err := token.ParsePolicy(raw, "fixture-api-key")
	require.NoError(t, err)
	return manager
}
