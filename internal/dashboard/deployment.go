package dashboard

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode"

	"hello/internal/deployment"
	"hello/internal/ingress"
	"hello/internal/jsonobject"
)

// LoadDeployment reads only the compiled dashboard policy path with deployment
// ownership, ancestor, permission and ACL checks. No environment override applies.
func LoadDeployment() (Runtime, error) {
	raw, err := deployment.Read(deployment.Dashboard)
	if err != nil {
		return Runtime{}, err
	}
	return parseDeployment(raw)
}

func parseDeployment(raw []byte) (Runtime, error) {
	var origin, upstream, key, ca, cert, private, serverName string
	var gatewayJSON, monitoringJSON json.RawMessage
	var capacity int
	if jsonobject.Decode(raw, map[string]any{"origin": &origin, "upstream": &upstream, "api_key": &key,
		"upstream_ca_pem": &ca, "upstream_client_certificate_pem": &cert, "upstream_client_key_pem": &private,
		"upstream_server_name": &serverName, "ingress": &gatewayJSON, "monitoring": &monitoringJSON, "max_sessions": &capacity}) != nil {
		return Runtime{}, deployment.ErrConfiguration
	}
	if len(key) < 32 || len(key) > 4096 || strings.TrimSpace(key) != key || strings.ContainsFunc(key, unicode.IsControl) || serverName == "" || capacity < 1 || capacity > 10000 {
		return Runtime{}, deployment.ErrConfiguration
	}
	roots, err := deployment.CertificatePool(ca)
	if err != nil {
		return Runtime{}, deployment.ErrConfiguration
	}
	identity, err := tls.X509KeyPair([]byte(cert), []byte(private))
	if err != nil {
		return Runtime{}, deployment.ErrConfiguration
	}
	gateway, err := ingress.Parse(gatewayJSON)
	if err != nil {
		return Runtime{}, deployment.ErrConfiguration
	}
	monitoring, err := deployment.ParsePrivateListener(monitoringJSON)
	if err != nil {
		return Runtime{}, deployment.ErrConfiguration
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: serverName, Certificates: []tls.Certificate{identity}}
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 10 * time.Second
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	config := Config{Origin: origin, Upstream: upstream, APIKey: key, MaxSessions: capacity, VerifiedClientIP: gateway.ClientIP}
	if _, err := New(config, client); err != nil {
		return Runtime{}, deployment.ErrConfiguration
	}
	return Runtime{Config: config, Assets: "/app", Client: client, Gateway: gateway, Diagnostics: monitoring}, nil
}
