package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDashboardNewQRDoesNotRevokePairedDeviceSession(t *testing.T) {
	s := &issuerServer{cfg: testConfig(t), managed: true, sessions: make(map[[sha256.Size]byte]issuerSession)}
	directory, err := os.MkdirTemp("/tmp", "hello-pair-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "pair.sock")
	closeControl, err := s.startDashboardControl(path, "https://192.168.1.15:8443")
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
