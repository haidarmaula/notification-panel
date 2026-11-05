//go:build integration

package fcm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type oauthFixtureTransport struct {
	base  *http.Transport
	calls atomic.Int32
}

func (f *oauthFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost || r.URL.String() != oauthEndpoint {
		return nil, errors.New("non-fixture OAuth forbidden")
	}
	f.calls.Add(1)
	cloned := r.Clone(r.Context())
	cloned.URL = &url.URL{Scheme: "http", Host: "127.0.0.1:14010", Path: "/oauth/token"}
	cloned.Host = ""
	return f.base.RoundTrip(cloned)
}
func TestFileCredentialsAgainstSimulation(t *testing.T) {
	if os.Getenv("HELLO_INTEGRATION") != "1" {
		t.Skip("run bash scripts/integration.sh")
	}
	data, policy, _ := fileFixture(t)
	for _, kid := range []string{"synthetic-kid", "fixture-denied", "fixture-timeout"} {
		t.Run(kid, func(t *testing.T) {
			var fields map[string]string
			if json.Unmarshal(data, &fields) != nil {
				t.Fatal("fixture decode")
			}
			fields["private_key_id"] = kid
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			config, request := transportFixture(t)
			config.ProjectID = "integration-fixture"
			policy.CrossProjectApproved = true
			oauth := &oauthFixtureTransport{base: &http.Transport{Proxy: nil, Protocols: http1Protocols()}}
			defer oauth.base.CloseIdleConnections()
			source, cleanup, err := fileSource(config, policy, raw, oauth)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			fcm := &fixtureTransport{base: &http.Transport{Proxy: nil, Protocols: http1Protocols()}}
			client, err := newClient(config, source, fcm, fcm.base.CloseIdleConnections)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			p, err := client.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if kid == "fixture-timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			out := p.Send(ctx, request)
			if kid == "synthetic-kid" {
				if out.Disposition != Accepted || oauth.calls.Load() != 1 || fcm.calls.Load() != 1 {
					t.Fatal(out)
				}
				if out := send(t, client, request); out.Disposition != Accepted || oauth.calls.Load() != 1 || fcm.calls.Load() != 2 {
					t.Fatal("cached credential was not reused")
				}
			} else if out.Disposition != NotSubmitted || out.PhysicalRequests != 0 || oauth.calls.Load() != 1 || fcm.calls.Load() != 0 {
				t.Fatal("credential failure reached provider")
			}
		})
	}
}
