package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPairingQRLinkUsesLocalConnectionDetails(t *testing.T) {
	link := pairingLink("http://127.0.0.1:8082", "http://127.0.0.1:8181", "10000000-0000-4000-8000-000000000003", strings.Repeat("a", 43))
	want := "hellofcmsmoke://pair?api=http%3A%2F%2F127.0.0.1%3A8082&app=10000000-0000-4000-8000-000000000003&code=" +
		strings.Repeat("a", 43) + "&issuer=http%3A%2F%2F127.0.0.1%3A8181"
	if link != want {
		t.Fatal("pairing link does not match the Android scanner contract")
	}
	var output bytes.Buffer
	if err := writePairingQR(&output, link); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "█") || strings.Contains(output.String(), "hellofcmsmoke://") {
		t.Fatal("expected a scannable QR without a plaintext link")
	}
}

func TestLoopbackHTTPOriginRejectsUntrustedQRDestinations(t *testing.T) {
	for _, origin := range []string{"http://127.0.0.1:8082", "http://[::1]:8082"} {
		if !loopbackHTTPOrigin(origin) {
			t.Errorf("rejected loopback origin %q", origin)
		}
	}
	for _, origin := range []string{"http://192.168.1.1:8082", "http://127.0.0.1:0", "http://127.0.0.1:8082/",
		"http://127.0.0.1:8082?extra=1", "http://user@127.0.0.1:8082", "https://127.0.0.1:8082"} {
		if loopbackHTTPOrigin(origin) {
			t.Errorf("accepted unsupported origin %q", origin)
		}
	}
}
