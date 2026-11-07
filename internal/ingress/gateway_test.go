package ingress

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"hello/internal/ratelimit"
)

func testGateway(t *testing.T) *Gateway {
	t.Helper()
	limit := func(n int) *ratelimit.Window { w, err := ratelimit.New(n); require.NoError(t, err); return w }
	return &Gateway{peers: map[netip.Addr]bool{netip.MustParseAddr("10.0.0.5"): true}, active: make(chan struct{}, 1), authIP: limit(1), authTotal: limit(2), perIP: limit(6)}
}
func gatewayRequest(path, client string) *http.Request {
	r := httptest.NewRequest("POST", path, nil)
	r.RemoteAddr = "10.0.0.5:20000"
	r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-For", client)
	return r
}
func TestGatewayClientIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"untrusted private peer", func(r *http.Request) { r.RemoteAddr = "10.0.0.6:123" }},
		{"no backend TLS", func(r *http.Request) { r.TLS = nil }},
		{"unverified certificate", func(r *http.Request) { r.TLS.VerifiedChains = nil }},
		{"duplicate scheme", func(r *http.Request) { r.Header.Add("X-Forwarded-Proto", "https") }},
		{"external plaintext", func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "http") }},
		{"duplicate address", func(r *http.Request) { r.Header.Add("X-Forwarded-For", "192.0.2.1") }},
		{"chain", func(r *http.Request) { r.Header.Set("X-Forwarded-For", "192.0.2.1, 10.0.0.5") }},
		{"scope zone", func(r *http.Request) { r.Header.Set("X-Forwarded-For", "fe80::1%eth0") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := testGateway(t)
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/test", func(w http.ResponseWriter, r *http.Request) { t.Fatal("untrusted request reached handler") })
			r := gatewayRequest("/api/test", "192.0.2.1")
			tc.mutate(r)
			w := httptest.NewRecorder()
			g.Protect(mux).ServeHTTP(w, r)
			require.Equal(t, 403, w.Code)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		})
	}
}
func TestGatewayBudgets(t *testing.T) {
	g := testGateway(t)
	mux := http.NewServeMux()
	for _, pattern := range []string{"POST /api/v1/auth/login", "POST /api/v1/auth/refresh", "POST /api/v1/other"} {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	}
	serve := g.Protect(mux)
	post := func(path, client string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		serve.ServeHTTP(w, gatewayRequest(path, client))
		require.Equal(t, want, w.Code)
		if want == 429 || want == 503 {
			require.NotEmpty(t, w.Header().Get("Retry-After"))
		}
	}
	post("/api/v1/auth/login", "192.0.2.1", 204)
	post("/api/v1/auth/%6cogin", "192.0.2.1", 429)
	post("/api/v1/auth/refresh", "192.0.2.2", 204)
	post("/api/v1/auth/login", "192.0.2.3", 429)
	g.active <- struct{}{}
	post("/api/v1/other", "192.0.2.3", 503)
	<-g.active
	post("/api/v1/other", "192.0.2.3", 204)
	for range 6 {
		post("/api/v1/other", "192.0.2.4", 204)
	}
	post("/api/v1/other", "192.0.2.4", 429)
}
