package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const lanCAName = "hello-lan-ca.crt"

type lanPairing struct {
	tls          *tls.Config
	backend      *url.URL
	control      string
	publicOrigin string
}

// publicPairingOrigin accepts only HTTPS DNS origins; the public APK variant
// trusts system CAs, so IP literals and cleartext are never pairable there.
func publicPairingOrigin(value string) bool {
	origin, err := url.Parse(value)
	if err != nil || origin.Scheme != "https" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	host := origin.Hostname()
	if host == "" || !strings.Contains(host, ".") || strings.Trim(host, ".") != host {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-') {
				return false
			}
		}
	}
	if origin.Path != "" && origin.Path != "/" {
		return false
	}
	if port := origin.Port(); port != "" {
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil || number == 0 {
			return false
		}
	}
	return true
}

func runLANServer(cfg issuerConfig, address, directory, backend, control, publicOrigin string) error {
	endpoint, err := netip.ParseAddrPort(address)
	if err != nil || !endpoint.Addr().Is4() || !endpoint.Addr().IsPrivate() || endpoint.Port() == 0 {
		return errors.New("LAN pairing requires a private IPv4 address assigned to this Mac and an explicit port")
	}
	if !loopbackHTTPOrigin(backend) {
		return errors.New("LAN pairing requires a loopback mobile API upstream")
	}
	if publicOrigin != "" && !publicPairingOrigin(publicOrigin) {
		return errors.New("public pairing origin must be an HTTPS DNS origin without a path")
	}
	upstream, err := url.Parse(backend)
	if err != nil {
		return err
	}
	certificate, err := lanCertificate(directory, endpoint.Addr())
	if err != nil {
		return fmt.Errorf("prepare LAN pairing certificate: %w", err)
	}
	fmt.Fprintln(os.Stdout, "Android CA certificate:", filepath.Join(directory, lanCAName))
	lan := &lanPairing{tls: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}, backend: upstream, control: control, publicOrigin: publicOrigin}
	if err := runPairingServer(cfg, endpoint.String(), "", lan); err != nil {
		return fmt.Errorf("serve LAN pairing on %s: %w", endpoint, err)
	}
	return nil
}

func (lan *lanPairing) handler(issuer *issuerServer) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(lan.backend) },
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
			ResponseHeaderTimeout: 5 * time.Second,
			IdleConnTimeout:       30 * time.Second,
		},
		ErrorLog: log.New(io.Discard, "", 0),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", issuer.handleSession)
	mux.HandleFunc("POST /v1/origin", issuer.handleOrigin)
	prefix := "POST /api/v1/apps/" + issuer.cfg.AppID + "/mobile/installations"
	mux.Handle(prefix, proxy)
	for _, action := range []string{"refresh", "logout", "switch", "state"} {
		mux.Handle(prefix+"/{installation_id}/"+action, proxy)
	}
	return mux
}

func lanCertificate(directory string, ip netip.Addr) (tls.Certificate, error) {
	root, rootKey, err := loadOrCreateLANCA(directory)
	if err != nil {
		return tls.Certificate{}, err
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	leaf := &x509.Certificate{
		Subject:     pkix.Name{CommonName: "Hello local mobile API"},
		NotBefore:   now.Add(-time.Minute),
		NotAfter:    now.Add(7 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IP(ip.AsSlice())},
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, root, &privateKey.PublicKey, rootKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey}, nil
}

func loadOrCreateLANCA(directory string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return createLANCA(directory)
	}
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, nil, errors.New("LAN certificate directory must be private")
	}
	cert, err := os.ReadFile(filepath.Join(directory, lanCAName))
	if err != nil {
		return nil, nil, err
	}
	keyPath := filepath.Join(directory, "ca.key")
	keyInfo, err := os.Lstat(keyPath)
	if err != nil || !keyInfo.Mode().IsRegular() || keyInfo.Mode().Perm()&0o077 != 0 {
		return nil, nil, errors.New("LAN CA key must be a private regular file")
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	root, err := x509.ParseCertificate(cert)
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(key)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, nil, errors.New("invalid LAN CA key")
	}
	privateKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil || !privateKey.PublicKey.Equal(root.PublicKey) || !root.IsCA || root.CheckSignatureFrom(root) != nil {
		return nil, nil, errors.New("LAN CA key and certificate do not match")
	}
	if time.Until(root.NotAfter) < 7*24*time.Hour {
		return nil, nil, errors.New("LAN CA is expiring; rotate the LAN certificate directory and reinstall the CA on the test phone")
	}
	return root, privateKey, nil
}

func createLANCA(directory string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	root := &x509.Certificate{
		Subject:               pkix.Name{CommonName: "Hello local pairing CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.AddDate(1, 0, 0),
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, root, root, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, err
	}
	key, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return nil, nil, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(directory), ".lan-ca-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(staging)
	if err := os.WriteFile(filepath.Join(staging, lanCAName), der, 0o644); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(staging, "ca.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: key}), 0o600); err != nil {
		return nil, nil, err
	}
	if err := os.Rename(staging, directory); err != nil {
		return nil, nil, err
	}
	certificate, err := x509.ParseCertificate(der)
	return certificate, privateKey, err
}
