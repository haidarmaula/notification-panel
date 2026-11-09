package observability

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/fcm"
)

func TestHealthTransitionsAndOutputDeadline(t *testing.T) {
	now := time.Now()
	s := New("recovery")
	require.Equal(t, "database_stale", s.reason(now))
	s.db = dbSample{at: now, ok: true, hold: true}
	require.Equal(t, "loop_unavailable", s.reason(now))
	s.Phase("recovery", "running", now.Add(time.Second))
	require.Equal(t, "ready", s.reason(now))
	require.Equal(t, "loop_unavailable", s.reason(now.Add(4*time.Second)))
	s.Phase("recovery", "error", now.Add(time.Minute))
	require.Equal(t, "ready", s.reason(now.Add(5*time.Second)))
	require.Equal(t, "database_stale", s.reason(now.Add(16*time.Second)))
	s.draining = true
	require.Equal(t, "draining", s.reason(now))
	rd, wr := net.Pipe()
	defer rd.Close()
	defer wr.Close()
	drops := 0
	_, err := BoundedOutput(wr, func() { drops++ }).Write([]byte("log"))
	require.Error(t, err)
	require.Equal(t, 1, drops)
}

func TestProviderSeriesAndCancel(t *testing.T) {
	s := New()
	provider := s.Provider()
	provider.PhysicalStart()
	provider.PhysicalFinish(429, time.Millisecond)
	provider.LogicalResult(fcm.Rejected, fcm.Quota, time.Millisecond)
	provider.PermitDeferred(fcm.DeferralQuota)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	handler := Access(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "done") }), s.Observe)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, "POST", "/", nil))
	w := httptest.NewRecorder()
	s.handlers().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	for _, expected := range []string{
		`hello_provider_requests_total{status_class="4xx"} 1`,
		`hello_provider_results_total{disposition="rejected",reason="quota"} 1`,
		`hello_provider_permit_deferrals_total{reason="quota"} 1`,
		`hello_provider_permit_deferrals_total{reason="breaker"} 0`,
		"hello_provider_inflight 0", `outcome="cancelled"`,
	} {
		require.Contains(t, w.Body.String(), expected)
	}
}

func TestHTTPHistogramExposesApprovedP95Boundary(t *testing.T) {
	s := New()
	s.Observe(Request{
		Method:   http.MethodGet,
		Route:    "/api/v1/staff",
		Outcome:  "complete",
		Status:   http.StatusOK,
		Duration: 300 * time.Millisecond,
	})

	w := httptest.NewRecorder()
	s.handlers().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	require.Contains(t, w.Body.String(), `hello_http_request_duration_seconds_bucket{route="/api/v1/staff",le="0.3"} 1`)
}

func TestRejectPublicManagementAddresses(t *testing.T) {
	for _, addr := range []string{"localhost:9095", "0.0.0.0:9095", "[::]:9095", "8.8.8.8:443", "127.0.0.1:0"} {
		data := fmt.Sprintf(`{"listen":%q,"server_certificate_pem":"","server_key_pem":"","client_ca_pem":""}`, addr)
		_, err := ParseAdmin([]byte(data))
		require.ErrorIs(t, err, errAdmin)
	}
	a, err := ParseAdmin(nil)
	require.NoError(t, err)
	require.Nil(t, a)
}
