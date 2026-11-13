package dashboard

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func policyFixture(t *testing.T) map[string]any {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	private := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}))
	endpoint := func(port string) map[string]any {
		return map[string]any{"listen": "127.0.0.1:" + port, "server_certificate_pem": cert, "server_key_pem": private, "client_ca_pem": cert}
	}
	return map[string]any{"origin": "https://dashboard.example.invalid", "upstream": "https://127.0.0.1:8080", "api_key": strings.Repeat("k", 32), "upstream_ca_pem": cert, "upstream_client_certificate_pem": cert, "upstream_client_key_pem": private, "upstream_server_name": "localhost", "max_sessions": 10, "ingress": map[string]any{"endpoint": endpoint("18443"), "gateway_ips": []string{"127.0.0.1"}, "ip_per_minute": 60, "auth_ip_per_minute": 2, "auth_total_per_minute": 10, "concurrent_requests": 8}, "monitoring": endpoint("19105")}
}
func TestDeploymentPolicyPinsBothTLSBoundaries(t *testing.T) {
	fixture := policyFixture(t)
	raw, _ := json.Marshal(fixture)
	runtime, err := parseDeployment(raw)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Development || runtime.Gateway == nil || runtime.Diagnostics == nil || runtime.Assets != "/app" || runtime.VerifiedClientIP == nil {
		t.Fatal("missing deployment boundary")
	}
	transport := runtime.Client.Transport.(*http.Transport)
	if transport.Proxy != nil || transport.TLSClientConfig.RootCAs == nil || len(transport.TLSClientConfig.Certificates) != 1 || transport.TLSClientConfig.ServerName != "localhost" {
		t.Fatal("upstream identity not pinned")
	}
	for _, field := range []string{"ingress", "monitoring", "api_key", "upstream_ca_pem", "upstream_client_key_pem", "upstream_server_name", "max_sessions"} {
		changed := policyFixture(t)
		delete(changed, field)
		raw, _ := json.Marshal(changed)
		if _, err := parseDeployment(raw); err == nil {
			t.Fatal("accepted missing", field)
		}
	}
	fixture["development"] = true
	raw, _ = json.Marshal(fixture)
	if _, err := parseDeployment(raw); err == nil {
		t.Fatal("deployment accepted developer override")
	}
}
func TestUpstreamUsesVerifiedClientIPOnly(t *testing.T) {
	received := ""
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(200)
	}))
	defer api.Close()
	s, err := New(Config{Origin: "https://dashboard.example.invalid", Upstream: api.URL, APIKey: "key", VerifiedClientIP: func(*http.Request) string { return "192.0.2.5" }}, api.Client())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/bff/api/apps", nil)
	r.Header.Set("X-Forwarded-For", "forged")
	if _, _, err := s.call(r, "GET", "/api/v1/apps", "", nil, ""); err != nil || received != "192.0.2.5" {
		t.Fatal("unverified address forwarded")
	}
}

func TestDeployedRuntimeRequiresMTLSAndShutsDownCleanly(t *testing.T) {
	fixture := policyFixture(t)
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != strings.Repeat("k", 32) || r.Header.Get("X-Forwarded-Proto") != "https" {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(401)
	}))
	endpoint := fixture["ingress"].(map[string]any)["endpoint"].(map[string]any)
	cert, err := tls.X509KeyPair([]byte(endpoint["server_certificate_pem"].(string)), []byte(endpoint["server_key_pem"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(fixture["upstream_ca_pem"].(string)))
	upstream.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, MinVersion: tls.VersionTLS13}
	upstream.StartTLS()
	defer upstream.Close()
	fixture["upstream"] = upstream.URL
	freeAddress := func() string {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		listener.Close()
		return address
	}
	publicAddress, privateAddress := freeAddress(), freeAddress()
	endpoint["listen"] = publicAddress
	fixture["monitoring"].(map[string]any)["listen"] = privateAddress
	raw, _ := json.Marshal(fixture)
	runtime, err := parseDeployment(raw)
	if err != nil {
		t.Fatal(err)
	}
	bff, err := New(runtime.Config, runtime.Client)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/bff/", bff)
	mux.Handle("POST /bff/login", bff)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Serve(ctx, mux) }()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	ready := false
	for range 50 {
		response, err := client.Get("https://" + privateAddress + "/readyz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("dashboard readiness never became healthy")
	}
	anonymousTransport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}}
	defer anonymousTransport.CloseIdleConnections()
	anonymousClient := &http.Client{Transport: anonymousTransport, Timeout: time.Second}
	if response, err := anonymousClient.Get("https://" + publicAddress + "/bff/session"); err == nil {
		response.Body.Close()
		t.Fatal("unauthenticated TLS client reached BFF")
	}
	request, _ := http.NewRequest("GET", "https://"+publicAddress+"/bff/session", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-For", "192.0.2.5")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || !response.Cookies()[0].Secure {
		t.Fatal("verified gateway session failed")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("runtime did not join listeners")
	}
}
