//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"hello/internal/features/applications"
	"hello/internal/installation"
)

func TestMobileReceiptHTTPAuthenticatesProjectAndAcknowledgesDuplicates(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	f := newObservationFixture(t, ctx, "execution-address-0")
	var otherApp, otherUser int64
	var other pgtype.UUID
	require.NoError(t, f.pool.QueryRow(ctx, `INSERT INTO applications(key,name,status)
		VALUES('receipt-http-other','Other receipt app','ACTIVE') RETURNING id,public_id`).Scan(&otherApp, &other))
	require.NoError(t, f.pool.QueryRow(ctx, "INSERT INTO users(application_id,external_id) VALUES($1,'recipient') RETURNING id", otherApp).Scan(&otherUser))
	executionSQL(t, ctx, f.executionFixture, `INSERT INTO mobile_identities(application_id,identity_namespace,subject,user_id)
		VALUES($1,'fixture-subjects','recipient',$2)`, otherApp, otherUser)
	mobileKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{33}, 32))
	originKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{34}, 32)).Public().(ed25519.PublicKey)
	receiptKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{11}, 32)).Public().(ed25519.PublicKey)
	project := func(id pgtype.UUID, audience string) map[string]any {
		return map[string]any{
			"app_id": id.String(), "issuer": "mobile", "audience": audience, "purpose": "access", "namespace": "fixture-subjects",
			"verification_key": mobileKey.Public().(ed25519.PublicKey), "origin_issuer": "origin", "origin_audience": "enroll", "origin_key": originKey,
			"routes": []map[string]any{{"platform": "ANDROID", "address_type": installation.FCMRegistrationToken, "provider_namespace": "execution-fixture", "provider_route_key": "route"}},
			"receipts": map[string]any{"environment": "fixture", "verification_keys": []map[string]any{{"id": "fixture-key", "key": receiptKey}},
				"trusted_tls_proxies": []string{}, "ip_per_minute": 600, "principal_per_minute": 120, "installation_per_minute": 120},
		}
	}
	policy, err := json.Marshal(map[string]any{
		"current_key_version": 1, "fingerprint_key": bytes.Repeat([]byte{2}, 32),
		"encryption_keys": []map[string]any{{"version": 1, "key": bytes.Repeat([]byte{1}, 32)}},
		"projects":        []map[string]any{project(f.project, "fixture-app"), project(other, "other-app")},
	})
	require.NoError(t, err)
	runtime, err := installation.LoadRuntime(bytes.NewReader(policy), f.q, f.pool)
	require.NoError(t, err)
	mux := http.NewServeMux()
	require.NoError(t, applications.RegisterMobileReceipts(mux, runtime, f.pool))
	bearer := func(audience string) string {
		t.Helper()
		token, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"iss": "mobile", "aud": audience, "sub": "recipient", "purpose": "access", "exp": time.Now().Add(time.Minute).Unix()}).SignedString(mobileKey)
		require.NoError(t, err)
		return token
	}
	record := func(app pgtype.UUID, token, event string) int {
		t.Helper()
		body, err := json.Marshal(map[string]any{"delivery_id": f.report.DeliveryID.String(), "client_event_id": f.report.ClientEventID.String(), "receipt_token": f.report.Token, "event": event})
		require.NoError(t, err)
		target := fmt.Sprintf("https://example.test/api/v1/apps/%s/mobile/notifications/%d/receipt", app.String(), f.campaign)
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		require.NotContains(t, recorder.Body.String(), f.report.Token)
		return recorder.Code
	}
	valid := bearer("fixture-app")
	require.Equal(t, http.StatusUnauthorized, record(f.project, "", "OPENED"))
	require.Equal(t, http.StatusUnauthorized, record(f.project, bearer("other-app"), "OPENED"))
	require.Equal(t, http.StatusNotFound, record(other, bearer("other-app"), "OPENED"))
	require.Equal(t, http.StatusOK, record(f.project, valid, "OPENED"))
	require.Equal(t, http.StatusOK, record(f.project, valid, "OPENED"))
	require.Equal(t, http.StatusConflict, record(f.project, valid, "DELIVERED"))
	require.Len(t, f.projections(t, ctx), 1)
	require.Equal(t, 1, f.readCount(t, ctx))
}
