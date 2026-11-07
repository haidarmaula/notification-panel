package deployment

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"strings"

	"github.com/segmentio/kafka-go/sasl/plain"
	"hello/internal/jsonobject"
	"hello/internal/kafka"
)

// Broker accepts only explicit authenticated TLS profiles. PLAIN is
// permitted solely inside verified TLS; mTLS never falls back to anonymous TLS.
func Broker(data []byte, topic string) (kafka.ScopedProducerConfig, error) {
	var none kafka.ScopedProducerConfig
	var brokers []string
	var mode, caPEM, certificatePEM, keyPEM, username, password string
	if jsonobject.Decode(data, map[string]any{
		"brokers": &brokers, "authentication": &mode, "ca_pem": &caPEM,
		"client_certificate_pem": &certificatePEM, "client_key_pem": &keyPEM,
		"sasl_username": &username, "sasl_password": &password,
	}) != nil {
		return none, ErrConfiguration
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if caPEM != "" {
		pool, err := CertificatePool(caPEM)
		if err != nil {
			return none, err
		}
		tlsConfig.RootCAs = pool
	}
	cfg := kafka.ScopedProducerConfig{Brokers: brokers, Topic: topic, TLS: tlsConfig}
	switch mode {
	case "MTLS":
		if certificatePEM == "" || keyPEM == "" || username != "" || password != "" {
			return none, ErrConfiguration
		}
		certificate, err := tls.X509KeyPair([]byte(certificatePEM), []byte(keyPEM))
		if err != nil {
			return none, ErrConfiguration
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	case "SASL_PLAIN":
		if certificatePEM != "" || keyPEM != "" || !validPolicyText(username) || len(password) == 0 || len(password) > 8192 || strings.ContainsRune(password, 0) {
			return none, ErrConfiguration
		}
		cfg.SASL = plain.Mechanism{Username: username, Password: password}
	default:
		return none, ErrConfiguration
	}
	if cfg.Validate() != nil {
		return none, ErrConfiguration
	}
	return cfg, nil
}

// CertificatePool reads an explicit PEM trust bundle and rejects trailing garbage.
func CertificatePool(data string) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	rest := []byte(data)
	for len(bytes.TrimSpace(rest)) > 0 {
		if !bytes.HasPrefix(bytes.TrimSpace(rest), []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, ErrConfiguration
		}
		block, next := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, ErrConfiguration
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, ErrConfiguration
		}
		pool.AddCert(cert)
		rest = next
	}
	if len(pool.Subjects()) == 0 {
		return nil, ErrConfiguration
	}

	return pool, nil
}
