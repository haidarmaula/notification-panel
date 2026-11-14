//go:build ignore

// Integration-certs creates disposable TLS material for the isolated fixture.
// Its keys are synthetic, short-lived and intentionally readable by containers;
// they must never be used as production credentials.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) != 2 && (len(os.Args) != 3 || os.Args[2] != "--local-dev") {
		fmt.Fprintln(os.Stderr, "usage: integration-certs <fixture-directory> [--local-dev]")
		os.Exit(2)
	}
	lifetime := 6 * time.Hour
	if len(os.Args) == 3 {
		lifetime = 7 * 24 * time.Hour
	}
	if err := generate(os.Args[1], lifetime); err != nil {
		fmt.Fprintln(os.Stderr, "fixture certificate generation failed")
		os.Exit(1)
	}
}
func generate(directory string, lifetime time.Duration) error {
	if err := os.Mkdir(directory, 0755); err != nil {
		return err
	}
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "hello-isolated-fixture-ca"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(lifetime), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), 0644); err != nil {
		return err
	}
	for i, name := range []string{"server", "client", "provider"} {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		cert := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: "hello-fixture-" + name}, NotBefore: root.NotBefore, NotAfter: root.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		if name == "server" {
			cert.DNSNames = []string{"localhost", "kafka", "postgres"}
			cert.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		}
		if name == "provider" {
			cert.DNSNames = []string{"oauth2.googleapis.com", "fcm.googleapis.com"}
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		}
		der, err := x509.CreateCertificate(rand.Reader, cert, root, &key.PublicKey, rootKey)
		if err != nil {
			return err
		}
		pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return err
		}
		certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		private := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
		for suffix, data := range map[string][]byte{".crt": certificate, ".key": private, ".pem": append(append([]byte(nil), private...), certificate...)} {
			if err := os.WriteFile(filepath.Join(directory, name+suffix), data, 0644); err != nil {
				return err
			}
		}
	}
	return generateAccount(directory)
}

func generateAccount(directory string) error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	data, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "integration-fixture", "private_key_id": "worker-fixture",
		"client_email": "fixture@integration-fixture.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})),
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "service-account.json"), data, 0600); err != nil {
		return err
	}
	public := pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey)})
	return os.WriteFile(filepath.Join(directory, "oauth-public.pem"), public, 0644)
}
