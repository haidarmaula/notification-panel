package applications

import (
	"crypto/tls"
	"github.com/stretchr/testify/require"
	"hello/internal/installation"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReceiptClientUsesAuthenticatedTransport(t *testing.T) {
	for _, tc := range []struct {
		addr       string
		secure, ok bool
	}{
		{"192.0.2.5:1234", false, false}, {"192.0.2.5:1234", true, true}, {"invalid-address", true, false},
	} {
		r := httptest.NewRequest("POST", "http://example.test/", nil)
		r.RemoteAddr = tc.addr
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "198.51.100.1")
		if tc.secure {
			r.TLS = &tls.ConnectionState{}
		}
		client, ok := receiptClient(r, installation.ReceiptPolicy{})
		require.Equal(t, tc.ok, ok)
		if ok {
			require.Equal(t, "192.0.2.5", client.String())
		}
	}
	w := httptest.NewRecorder()
	receiptLimited(w, int(time.Minute.Seconds()))
	require.Equal(t, 429, w.Code)
	require.Equal(t, "60", w.Header().Get("Retry-After"))
}
