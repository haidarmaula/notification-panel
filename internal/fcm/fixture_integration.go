//go:build integration

package fcm

import (
	"errors"
	"net/http"
	"net/url"

	"cloud.google.com/go/auth"
)

// NewIntegrationClient is available only in opt-in integration builds. It routes
// the synthetic project to fixed loopback Postman, never a real provider. The
// caller supplies synthetic tokens only. Production builds expose no such seam.
func NewIntegrationClient(config Config, source auth.TokenProvider) (*Client, error) {
	if config.ProjectID != "integration-fixture" {
		return nil, ErrConfiguration
	}
	base := &http.Transport{Proxy: nil, Protocols: http1Protocols()}
	client, err := newClient(config, source, integrationHTTP{base: base}, base.CloseIdleConnections)
	if err != nil {
		base.CloseIdleConnections()
	}
	return client, err
}

type integrationHTTP struct{ base *http.Transport }

func (t integrationHTTP) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost || r.URL.String() != "https://fcm.googleapis.com/v1/projects/integration-fixture/messages:send" || r.Header.Get("Authorization") != "Bearer synthetic_access_token" {
		return nil, errors.New("non-fixture request forbidden")
	}
	cloned := r.Clone(r.Context())
	cloned.URL = &url.URL{Scheme: "http", Host: "127.0.0.1:14010", Path: "/fcm/v1/projects/integration-fixture/messages:send"}
	cloned.Host = ""
	cloned.Header.Del("Authorization")
	return t.base.RoundTrip(cloned)
}
