//go:build integration

package fcm

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"hello/internal/installation"
)

// Only the private test seam can rewrite the exact production target. No real
// access token, device or externally selected endpoint reaches this fixture.
type fixtureTransport struct {
	base  *http.Transport
	calls atomic.Int32
}

func (f *fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost || r.URL.String() != "https://fcm.googleapis.com/v1/projects/integration-fixture/messages:send" {
		return nil, errors.New("non-fixture request forbidden")
	}
	f.calls.Add(1)
	cloned := r.Clone(r.Context())
	cloned.URL = &url.URL{Scheme: "http", Host: "127.0.0.1:14010", Path: "/fcm/v1/projects/integration-fixture/messages:send"}
	cloned.Host = ""
	cloned.Header.Del("Authorization")
	return f.base.RoundTrip(cloned)
}

func TestFCMTransportAgainstSimulation(t *testing.T) {
	if os.Getenv("HELLO_INTEGRATION") != "1" {
		t.Skip("run bash scripts/integration.sh")
	}
	for _, platform := range []string{"ANDROID", "IOS"} {
		for _, addressType := range []string{installation.FCMRegistrationToken, installation.FCMFID} {
			t.Run(platform+"/"+addressType, func(t *testing.T) {
				config, request := transportFixture(t)
				config.ProjectID = "integration-fixture"
				config.Routes[0].Platform, config.Routes[0].AddressType = platform, addressType
				request.Destination.Platform, request.Destination.AddressType = platform, addressType
				rt := &fixtureTransport{base: &http.Transport{Proxy: nil, Protocols: http1Protocols()}}
				c, err := newClient(config, tokenFunc(fixtureToken), rt, rt.base.CloseIdleConnections)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				out := send(t, c, request)
				if out.Disposition != Accepted || out.Reason != Success || out.PhysicalRequests != 1 || rt.calls.Load() != 1 {
					t.Fatal(out)
				}
			})
		}
	}
	for _, tc := range []struct {
		address     string
		disposition Disposition
		reason      Reason
		floor       time.Duration
	}{
		{"fixture-unregistered", Rejected, Unregistered, 0},
		{"fixture-quota", Rejected, Quota, time.Minute},
		{"fixture-unavailable", Rejected, Unavailable, 10 * time.Second},
		{"fixture-timeout", Unconfirmed, Network, 10 * time.Second},
	} {
		t.Run(tc.address, func(t *testing.T) {
			config, request := transportFixture(t)
			config.ProjectID = "integration-fixture"
			request.Destination.Address = tc.address
			rt := &fixtureTransport{base: &http.Transport{Proxy: nil, Protocols: http1Protocols()}}
			c, err := newClient(config, tokenFunc(fixtureToken), rt, rt.base.CloseIdleConnections)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			p, err := c.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if tc.address == "fixture-timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			started := time.Now()
			out := p.Send(ctx, request)
			if out.Disposition != tc.disposition || out.Reason != tc.reason || out.PhysicalRequests != 1 || rt.calls.Load() != 1 {
				t.Fatalf("outcome %+v requests=%d", out, rt.calls.Load())
			}
			if time.Since(started) > time.Second {
				t.Fatal("hidden retry or timeout wait")
			}
			if tc.floor > 0 && out.RetryAt.Before(started.Add(tc.floor)) {
				t.Fatal("retry floor shortened")
			}
		})
	}
}
