//go:build integration

package integration

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/internal/observability"
)

func TestPrivateDiagnostics(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pool := acceptanceFixture(t, ctx)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := l.Addr().String()
	require.NoError(t, l.Close())
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(os.Getenv("HELLO_INTEGRATION_CERTS_DIR"), name))
		require.NoError(t, err)
		return string(data)
	}
	raw, err := json.Marshal(map[string]string{"listen": address, "client_ca_pem": read("ca.crt"), "server_certificate_pem": read("server.crt"), "server_key_pem": read("server.key")})
	require.NoError(t, err)
	admin, err := observability.ParseAdmin(raw)
	require.NoError(t, err)
	state := observability.New("schedule_promotion")
	state.Phase("schedule_promotion", "running", time.Now().Add(time.Minute))
	stopped := make(chan error, 1)
	drain, release := context.WithCancel(t.Context())
	t.Cleanup(func() {
		release()
		cancel()
		select {
		case err := <-stopped:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("private listener did not exit")
		}
	})
	go func() {
		stopped <- state.Run(ctx, admin, pool, slog.Default(), func(ctx context.Context) error { <-ctx.Done(); <-drain.Done(); return nil })
	}()
	makeClient := func(config *tls.Config) *http.Client {
		tr := &http.Transport{TLSClientConfig: config}
		t.Cleanup(tr.CloseIdleConnections)
		return &http.Client{Transport: tr, Timeout: time.Second}
	}
	cert := admissionFixtureTLS(t)
	client := makeClient(cert)
	probe := func(path string) (int, string) {
		res, err := client.Get("https://" + address + path)
		if err != nil {
			return 0, ""
		}
		defer res.Body.Close()
		data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if err != nil {
			return 0, ""
		}
		return res.StatusCode, string(data)
	}
	require.Eventually(t, func() bool { c, _ := probe("/readyz"); return c == 200 }, 5*time.Second, 30*time.Millisecond)
	unauth := cert.Clone()
	unauth.Certificates = nil
	resp, err := makeClient(unauth).Get("https://" + address + "/metrics")
	if resp != nil {
		resp.Body.Close()
	}
	require.Error(t, err)
	code, _ := probe("/anything")
	require.Equal(t, 404, code)
	code, body := probe("/metrics")
	require.Equal(t, 200, code)
	require.Contains(t, body, "hello_dispatch_held 1")
	require.Eventually(t, func() bool {
		_, sample := probe("/metrics")
		return strings.Contains(sample, `hello_outbox_due_age_seconds{queue="send"} 0`) && strings.Contains(sample, `hello_work_items{family="waiting",kind="quarantine"} 0`)
	}, 2*time.Second, 30*time.Millisecond)
	state.Phase("schedule_promotion", "stopped", time.Time{})
	code, body = probe("/readyz")
	require.Equal(t, 503, code)
	require.Equal(t, "loop_unavailable\n", body)
	state.Phase("schedule_promotion", "running", time.Now().Add(time.Minute))
	_, err = pool.Exec(ctx, "ALTER TABLE notification_runtime RENAME TO unavailable_runtime")
	require.NoError(t, err)
	require.Eventually(t, func() bool { c, b := probe("/readyz"); return c == 503 && b == "database_unavailable\n" }, 8*time.Second, 100*time.Millisecond)
	code, _ = probe("/livez")
	require.Equal(t, 200, code)
	cancel()
	require.Eventually(t, func() bool { c, b := probe("/readyz"); return c == 503 && b == "draining\n" }, time.Second, 10*time.Millisecond)
	code, _ = probe("/livez")
	require.Equal(t, 200, code)
}
