//go:build integration

package integration

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/ingress"
	"hello/internal/middleware"
)

func TestAuthenticatedGatewayTransport(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(os.Getenv("HELLO_INTEGRATION_CERTS_DIR"), name))
		require.NoError(t, err)
		return string(data)
	}
	policy := map[string]any{
		"endpoint":    map[string]string{"listen": addr, "client_ca_pem": read("ca.crt"), "server_certificate_pem": read("server.crt"), "server_key_pem": read("server.key")},
		"gateway_ips": []string{"127.0.0.1"}, "ip_per_minute": 20, "auth_ip_per_minute": 2, "auth_total_per_minute": 4, "concurrent_requests": 2,
	}
	data, err := json.Marshal(policy)
	require.NoError(t, err)
	gw, err := ingress.Parse(data)
	require.NoError(t, err)
	l, err = gw.Listener.Listen(ctx)
	require.NoError(t, err)
	mux := http.NewServeMux()
	audit := &middleware.AuditMiddleware{VerifiedClientIP: gw.ClientIP}
	mux.HandleFunc("POST /api/v1/auth/login", audit.Use(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Context().Value(middleware.AuditIPKey).(string))
	}))
	server := &http.Server{Handler: gw.Protect(mux), ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(l) }()
	defer func() { require.NoError(t, server.Close()); require.ErrorIs(t, <-done, http.ErrServerClosed) }()
	cfg := admissionFixtureTLS(t)
	certificate := cfg.Certificates
	clientFor := func(c *tls.Config) *http.Client {
		tr := &http.Transport{TLSClientConfig: c}
		t.Cleanup(tr.CloseIdleConnections)
		return &http.Client{Transport: tr, Timeout: time.Second}
	}
	cfg.Certificates = nil
	request := func(client *http.Client) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://"+addr+"/api/v1/auth/login", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Forwarded-For", "192.0.2.200")
		req.Header.Set("X-Forwarded-Proto", "https")
		return client.Do(req)
	}
	resp, err := request(clientFor(cfg))
	if resp != nil {
		resp.Body.Close()
	}
	require.Error(t, err)
	auth := cfg.Clone()
	auth.Certificates = certificate
	client := clientFor(auth)
	for _, want := range []int{200, 200, 429} {
		resp, err = request(client)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, want, resp.StatusCode)
		if want == 200 {
			require.Equal(t, "192.0.2.200", string(body))
		} else {
			require.NotEmpty(t, resp.Header.Get("Retry-After"))
		}
	}
}
