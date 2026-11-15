package provisioning

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClientRequiresVerifiedTLSAndClientIdentity(t *testing.T) {
	serverName := "provisioner.test"
	roots := x509.NewCertPool()
	valid := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: serverName, Certificates: []tls.Certificate{{Certificate: [][]byte{{1}}, PrivateKey: struct{}{}}}}
	cases := []struct {
		name     string
		endpoint string
		config   *tls.Config
	}{
		{"missing TLS config", "https://provisioner.test", nil},
		{"HTTP endpoint", "http://provisioner.test", valid},
		{"skip verification", "https://provisioner.test", func() *tls.Config { c := valid.Clone(); c.InsecureSkipVerify = true; return c }()},
		{"old TLS", "https://provisioner.test", func() *tls.Config { c := valid.Clone(); c.MinVersion = tls.VersionTLS12; return c }()},
		{"missing roots", "https://provisioner.test", func() *tls.Config { c := valid.Clone(); c.RootCAs = nil; return c }()},
		{"missing client cert", "https://provisioner.test", func() *tls.Config { c := valid.Clone(); c.Certificates = nil; return c }()},
		{"wrong server name", "https://provisioner.test", func() *tls.Config { c := valid.Clone(); c.ServerName = "elsewhere.test"; return c }()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewClient(tc.endpoint, tc.config); err == nil {
				t.Fatal("NewClient accepted unsafe configuration")
			}
		})
	}
	client, err := NewClient("https://provisioner.test", valid)
	if err != nil {
		t.Fatal("NewClient rejected valid pinned mTLS configuration")
	}
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || transport.TLSClientConfig.MinVersion < tls.VersionTLS13 || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("client transport does not enforce pinned TLS without proxies")
	}
	if client.http.CheckRedirect == nil {
		t.Fatal("client may follow redirects")
	}
}

func TestClientPostsStageToFixedRoute(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://provisioner.test/v1/stages" || request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" {
			t.Fatal("client did not use the fixed stage route")
		}
		var input StageRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.ConfigID != testConfigID {
			t.Fatal("client sent an unexpected stage body")
		}
		body, _ := json.Marshal(StageResult{Reference: input.ConfigID, AppID: input.AppID, Environment: input.Environment, ConfigID: input.ConfigID, Size: int64(len(input.ServiceAccount))})
		return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})
	client := &Client{endpoint: "https://provisioner.test/v1/stages", http: &http.Client{Transport: transport}}
	result, err := client.Stage(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("Stage failed: %v", err)
	}
	if result.Reference != testConfigID || result.AppID != testAppID || result.Size == 0 {
		t.Fatalf("unexpected safe result: %+v", result)
	}
}

func TestClientUsesVerifiedMutualTLSToFixedRoute(t *testing.T) {
	serverCert := testTLSCertificate(t, "localhost")
	clientCert := testTLSCertificate(t, "client.test")
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(clientCert.Leaf)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/stages" || r.Method != http.MethodPost || len(r.TLS.PeerCertificates) != 1 {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var input StageRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(StageResult{Reference: input.ConfigID, AppID: input.AppID, Environment: input.Environment, ConfigID: input.ConfigID, Size: int64(len(input.ServiceAccount))})
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots, Certificates: []tls.Certificate{serverCert}}
	server.StartTLS()
	defer server.Close()
	serverRoots := x509.NewCertPool()
	serverRoots.AddCert(server.Certificate())
	endpoint := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	client, err := NewClient(endpoint, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: serverRoots, ServerName: "localhost", Certificates: []tls.Certificate{clientCert}})
	if err != nil {
		t.Fatal("NewClient rejected a valid mutual TLS configuration")
	}
	defer client.Close()
	result, err := client.Stage(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("Stage failed: %v", err)
	}
	if result.Reference != testConfigID || result.AppID != testAppID || result.Size == 0 {
		t.Fatalf("unexpected safe result: %+v", result)
	}
}

func testTLSCertificate(t *testing.T, commonName string) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("cannot generate test TLS key")
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: commonName}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, DNSNames: []string{commonName}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal("cannot create test TLS certificate")
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal("cannot parse test TLS certificate")
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: certificate}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
