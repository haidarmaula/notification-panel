package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDashboardNewQRDoesNotRevokePairedDeviceSession(t *testing.T) {
	s := &issuerServer{cfg: testConfig(t), managed: true, sessions: make(map[[sha256.Size]byte]issuerSession)}
	directory, err := os.MkdirTemp("/tmp", "hello-pair-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "pair.sock")
	closeControl, err := s.startDashboardControl(path, "https://192.168.1.15:8443", "")
	if err != nil {
		t.Fatal(err)
	}
	defer closeControl()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	handler := s.handler()
	newQR := func() string {
		t.Helper()
		response, err := client.Post("http://localhost/pair", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var envelope struct{ Data map[string]string }
		if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil || response.StatusCode != 200 || !strings.HasPrefix(envelope.Data["qr_svg"], "<svg") {
			t.Fatal("expected a dashboard pairing QR")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.pairingCode
	}
	accept := func(code string, want int) string {
		t.Helper()
		request := httptest.NewRequest("POST", "/v1/session", strings.NewReader(`{"app_id":"`+s.cfg.AppID+`","pairing_code":"`+code+`"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("session status %d, want %d", response.Code, want)
		}
		var envelope struct{ Data sessionResponse }
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data.Credential
	}
	first := newQR()
	firstCredential := accept(first, http.StatusOK)
	accept(first, http.StatusUnauthorized)
	second := newQR()
	accept(first, http.StatusUnauthorized)
	secondCredential := accept(second, http.StatusOK)
	for _, credential := range []string{firstCredential, secondCredential} {
		request := httptest.NewRequest("POST", "/v1/origin", strings.NewReader(`{"app_id":"`+s.cfg.AppID+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+credential)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatal("a paired device lost its session when a new QR was issued")
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/pair", nil))
	if response.Code != http.StatusNotFound {
		t.Fatal("LAN handler must not expose QR issuance")
	}
}

func TestDashboardPublicQRUsesConfiguredPublicOrigin(t *testing.T) {
	s := &issuerServer{cfg: testConfig(t), managed: true, sessions: make(map[[sha256.Size]byte]issuerSession)}
	directory, err := os.MkdirTemp("/tmp", "hello-public-pair-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "pair.sock")
	closeControl, err := s.startDashboardControl(path, "https://192.168.1.15:8443", "https://api.hello-staging.example")
	if err != nil {
		t.Fatal(err)
	}
	defer closeControl()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	pair := func(body string, want int) map[string]string {
		t.Helper()
		response, err := client.Post("http://localhost/pair", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("pair status %d, want %d", response.StatusCode, want)
		}
		if want != http.StatusOK {
			return nil
		}
		var envelope struct{ Data map[string]string }
		if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	public := pair(`{"public":true}`, http.StatusOK)
	if public["origin"] != "https://api.hello-staging.example" || !strings.HasPrefix(public["qr_svg"], "<svg") {
		t.Fatal("public QR must carry the configured public origin")
	}
	lan := pair(``, http.StatusOK)
	if lan["origin"] != "https://192.168.1.15:8443" {
		t.Fatal("default QR must keep the LAN origin")
	}
	pair(`{bad`, http.StatusBadRequest)

	unconfigured := &issuerServer{cfg: testConfig(t), managed: true, sessions: make(map[[sha256.Size]byte]issuerSession)}
	other := filepath.Join(directory, "other.sock")
	closeOther, err := unconfigured.startDashboardControl(other, "https://192.168.1.15:8443", "")
	if err != nil {
		t.Fatal(err)
	}
	defer closeOther()
	otherTransport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", other)
	}}
	defer otherTransport.CloseIdleConnections()
	otherClient := &http.Client{Transport: otherTransport}
	response, err := otherClient.Post("http://localhost/pair", "application/json", strings.NewReader(`{"public":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("public QR must be refused when no public origin is configured")
	}
}

func TestLoopbackIssuerWithControlSocketServesPublicEnrollment(t *testing.T) {
	cfg := testConfig(t)
	directory, err := os.MkdirTemp("/tmp", "hello-loopback-issuer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "control.sock")
	go func() {
		_ = runPairingServer(cfg, "127.0.0.1:18191", "", nil, socket, "https://api.hello-staging.example")
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("loopback control socket never appeared")
		}
		time.Sleep(20 * time.Millisecond)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	response, err := client.Post("http://localhost/pair", "application/json", strings.NewReader(`{"public":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope struct {
		Data struct {
			Link   string `json:"link"`
			Origin string `json:"origin"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil || response.StatusCode != 200 {
		t.Fatal("expected a public pairing link from the loopback control socket")
	}
	if envelope.Data.Origin != "https://api.hello-staging.example" {
		t.Fatal("public QR origin must be the configured public origin")
	}
	link, err := url.Parse(envelope.Data.Link)
	if err != nil {
		t.Fatal(err)
	}
	code := link.Query().Get("code")
	session, err := http.Post("http://127.0.0.1:18191/v1/session", "application/json",
		strings.NewReader(`{"app_id":"`+cfg.AppID+`","pairing_code":"`+code+`"}`))
	if err != nil || session.StatusCode != 200 {
		t.Fatal("loopback issuer must redeem the control-socket pairing code")
	}
	var issued struct {
		Data sessionResponse `json:"data"`
	}
	if err := json.NewDecoder(session.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	session.Body.Close()
	request, _ := http.NewRequest("POST", "http://127.0.0.1:18191/v1/origin", strings.NewReader(`{"app_id":"`+cfg.AppID+`"}`))
	request.Header.Set("Authorization", "Bearer "+issued.Data.Credential)
	request.Header.Set("Content-Type", "application/json")
	origin, err := http.DefaultClient.Do(request)
	if err != nil || origin.StatusCode != 200 {
		t.Fatal("loopback issuer must assert an enrollment origin for the paired credential")
	}
	origin.Body.Close()
}

func TestServeLoopbackControlFlagWiring(t *testing.T) {
	cfg := testConfig(t)
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp("/tmp", "hello-serve-flag-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	config := filepath.Join(directory, "issuer.json")
	if err := os.WriteFile(config, data, 0o600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(directory, "control.sock")
	go func() {
		_ = serve([]string{"--config", config, "--listen", "127.0.0.1:18192",
			"--control-socket", socket, "--public-origin", "https://api.hello-staging.example"})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("serve --control-socket never created the loopback control socket")
		}
		time.Sleep(20 * time.Millisecond)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	response, err := client.Post("http://localhost/pair", "application/json", strings.NewReader(`{"public":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope struct {
		Data struct {
			Origin string `json:"origin"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil || response.StatusCode != 200 {
		t.Fatal("expected a public pairing from the CLI-wired control socket")
	}
	if envelope.Data.Origin != "https://api.hello-staging.example" {
		t.Fatal("serve must forward --public-origin to the control socket")
	}
}
