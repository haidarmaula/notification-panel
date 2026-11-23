package main

import (
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLANCertificateRetainsTrustWhenMacIPChanges(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "lan")
	first := netip.MustParseAddr("192.168.1.10")
	second := netip.MustParseAddr("192.168.1.11")
	if _, err := lanCertificate(directory, first); err != nil {
		t.Fatal(err)
	}
	der, err := os.ReadFile(filepath.Join(directory, lanCAName))
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	pair, err := lanCertificate(directory, second)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: second.String()}); err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname(first.String()); err == nil {
		t.Fatal("accepted a certificate for a different IP address")
	}
}

func TestLANOnlyProxiesScopedMobileRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-mobile-token" || r.Header.Get("X-Forwarded-Proto") != "" {
			t.Error("proxy failed to preserve mobile auth or strip injected forwarding headers")
		}
		_, _ = io.WriteString(w, r.URL.Path)
	}))
	defer upstream.Close()
	backend, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig(t)
	handler := (&lanPairing{backend: backend}).handler(&issuerServer{cfg: config})
	prefix := "/api/v1/apps/" + config.AppID + "/mobile/installations"
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"POST", prefix, http.StatusOK},
		{"POST", prefix + "/test/refresh", http.StatusOK},
		{"GET", prefix, http.StatusMethodNotAllowed},
		{"POST", strings.Replace(prefix, config.AppID, "another-app", 1), http.StatusNotFound},
		{"POST", "/api/v1/auth/login", http.StatusNotFound},
		{"POST", "/v1/session", http.StatusBadRequest},
	} {
		request := httptest.NewRequest(tc.method, tc.path, nil)
		request.Header.Set("Authorization", "Bearer test-mobile-token")
		request.Header.Set("X-Forwarded-Proto", "injected")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.want {
			t.Errorf("%s %s: got %d, want %d", tc.method, tc.path, response.Code, tc.want)
		}
	}
}
