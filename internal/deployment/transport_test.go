package deployment

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func validBrokerPolicy() map[string]any {
	return map[string]any{"brokers": []string{"broker.example.test:9093"}, "authentication": "SASL_PLAIN", "ca_pem": "", "client_certificate_pem": "", "client_key_pem": "", "sasl_username": "replay", "sasl_password": "secret"}
}
func TestBrokerPolicyRejectsUnsafeTransport(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"anonymous", func(m map[string]any) { m["authentication"] = "TLS" }},
		{"insecure override", func(m map[string]any) { m["insecure_skip_verify"] = true }},
		{"missing SASL identity", func(m map[string]any) { m["sasl_username"] = "" }},
		{"NUL password", func(m map[string]any) { m["sasl_password"] = "a\x00b" }},
		{"bad CA", func(m map[string]any) { m["ca_pem"] = "invalid" }},
		{"no mTLS keypair", func(m map[string]any) { m["authentication"] = "MTLS"; m["sasl_username"] = ""; m["sasl_password"] = "" }},
		{"plaintext address", func(m map[string]any) { m["brokers"] = []string{"plaintext://host:9092"} }},
		{"duplicate address", func(m map[string]any) { m["brokers"] = []string{"host:9093", "host:9093"} }},
		{"no port", func(m map[string]any) { m["brokers"] = []string{"host"} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := validBrokerPolicy()
			tt.change(m)
			_, err := Broker([]byte(configurationJSON(t, m)), "send")
			if err != ErrConfiguration || strings.Contains(err.Error(), "secret") {
				t.Fatal("accepted unsafe policy or leaked secret")
			}
		})
	}
}
func TestBrokerPolicyAcceptsMTLS(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	m := validBrokerPolicy()
	m["authentication"] = "MTLS"
	m["ca_pem"] = cert
	m["client_certificate_pem"] = cert
	m["client_key_pem"] = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
	m["sasl_username"] = ""
	m["sasl_password"] = ""
	cfg, err := Broker([]byte(configurationJSON(t, m)), "send")
	if err != nil || len(cfg.TLS.Certificates) != 1 || cfg.TLS.RootCAs == nil || cfg.SASL != nil {
		t.Fatal("mTLS configuration rejected")
	}
	m["ca_pem"] = cert + "invalid trailing data"
	if _, err := Broker([]byte(configurationJSON(t, m)), "send"); err != ErrConfiguration {
		t.Fatal("accepted trailing invalid CA data")
	}
}

func configurationJSON(t *testing.T, value map[string]any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
