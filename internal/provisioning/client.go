package provisioning

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hello/internal/jsonobject"
)

const maxStageResponseSize = 8 * 1024

var errClientConfig = errors.New("invalid provisioning client configuration")
var errProvisioningUnavailable = errors.New("provisioning service unavailable")

// Client sends stage requests only to one configured HTTPS origin.
type Client struct {
	endpoint string
	http     *http.Client
}

// NewClient constructs a fixed-origin mTLS client. TLS must verify the server
// certificate and include a client identity and explicit trust roots.
func NewClient(endpoint string, tlsConfig *tls.Config) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		tlsConfig == nil || tlsConfig.InsecureSkipVerify || tlsConfig.MinVersion < tls.VersionTLS13 ||
		(tlsConfig.MaxVersion != 0 && tlsConfig.MaxVersion < tls.VersionTLS13) ||
		tlsConfig.RootCAs == nil || tlsConfig.ServerName == "" || !strings.EqualFold(tlsConfig.ServerName, u.Hostname()) ||
		len(tlsConfig.Certificates) == 0 || len(tlsConfig.Certificates[0].Certificate) == 0 || tlsConfig.Certificates[0].PrivateKey == nil {
		return nil, errClientConfig
	}
	tlsCopy := tlsConfig.Clone()
	tlsCopy.RootCAs = tlsConfig.RootCAs.Clone()
	tlsCopy.Certificates = append([]tls.Certificate(nil), tlsConfig.Certificates...)
	for i := range tlsCopy.Certificates {
		tlsCopy.Certificates[i].Certificate = cloneByteSlices(tlsConfig.Certificates[i].Certificate)
		tlsCopy.Certificates[i].OCSPStaple = bytes.Clone(tlsConfig.Certificates[i].OCSPStaple)
		tlsCopy.Certificates[i].SignedCertificateTimestamps = cloneByteSlices(tlsConfig.Certificates[i].SignedCertificateTimestamps)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = tlsCopy
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 10 * time.Second
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Client{endpoint: strings.TrimSuffix(endpoint, "/") + "/v1/stages", http: client}, nil
}

// Stage sends one reserved credential candidate and accepts only bounded safe
// metadata back from the configured service.
func (c *Client) Stage(ctx context.Context, input StageRequest) (StageResult, error) {
	if c == nil || c.http == nil || ctx == nil || ctx.Err() != nil || !validStageRequest(input) {
		return StageResult{}, errProvisioningUnavailable
	}
	body, err := json.Marshal(input)
	if err != nil || len(body) > maxStageRequestSize {
		return StageResult{}, errProvisioningUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return StageResult{}, errProvisioningUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return StageResult{}, errProvisioningUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return StageResult{}, errProvisioningUnavailable
	}
	response, err := io.ReadAll(io.LimitReader(resp.Body, maxStageResponseSize+1))
	if err != nil || len(response) > maxStageResponseSize {
		return StageResult{}, errProvisioningUnavailable
	}
	result := StageResult{}
	if jsonobject.Decode(response, map[string]any{
		"reference": &result.Reference, "application_id": &result.AppID,
		"environment": &result.Environment, "configuration_id": &result.ConfigID, "size": &result.Size,
	}) != nil || result.Reference != input.ConfigID || result.AppID != input.AppID || result.ConfigID != input.ConfigID ||
		result.Environment != input.Environment || result.Size < 1 || result.Size > maxCredentialSize {
		return StageResult{}, errProvisioningUnavailable
	}
	return result, nil
}

// Close releases idle connections owned by this client.
func (c *Client) Close() {
	if c == nil || c.http == nil {
		return
	}
	if transport, ok := c.http.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

func validStageRequest(input StageRequest) bool {
	return canonicalUUID(input.AppID) && canonicalUUID(input.ReservationID) && canonicalUUID(input.ConfigID) &&
		(input.Environment == "staging" || input.Environment == "production") &&
		len(input.ServiceAccount) > 0 && len(input.ServiceAccount) <= maxCredentialSize
}

func cloneByteSlices(values [][]byte) [][]byte {
	copyValues := make([][]byte, len(values))
	for i := range values {
		copyValues[i] = bytes.Clone(values[i])
	}
	return copyValues
}
