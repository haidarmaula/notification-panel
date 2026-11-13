package dashboard

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"hello/internal/deployment"
	"hello/internal/ingress"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// Runtime holds command configuration and the verified upstream HTTP client.
type Runtime struct {
	Config
	ListenAddress string
	Assets        string
	Client        *http.Client
	Gateway       *ingress.Gateway
	Diagnostics   *deployment.PrivateListener
}

// Load reads protected configuration and secrets once, without environment fallbacks.
func Load(path string) (Runtime, error) {
	var file struct {
		Origin      string `json:"origin"`
		Upstream    string `json:"upstream"`
		APIKeyFile  string `json:"api_key_file"`
		CAFile      string `json:"upstream_ca_file"`
		CertFile    string `json:"upstream_cert_file"`
		KeyFile     string `json:"upstream_key_file"`
		Listen      string `json:"listen_address"`
		Assets      string `json:"assets_directory"`
		Development bool   `json:"development"`
		MaxSessions int    `json:"max_sessions"`
	}
	raw, err := protected(path)
	if err != nil {
		return Runtime{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&file) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return Runtime{}, errors.New("invalid dashboard configuration")
	}
	key, err := protected(file.APIKeyFile)
	if err != nil {
		return Runtime{}, err
	}
	if len(key) == 0 || len(key) > 4096 {
		return Runtime{}, errors.New("invalid API key")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	if file.CAFile != "" {
		pem, readErr := os.ReadFile(file.CAFile)
		if readErr != nil {
			return Runtime{}, errors.New("cannot load upstream CA")
		}
		pool, loadErr := x509.SystemCertPool()
		if loadErr != nil {
			return Runtime{}, loadErr
		}
		if !pool.AppendCertsFromPEM(pem) {
			return Runtime{}, errors.New("invalid upstream CA")
		}
		tlsConfig.RootCAs = pool
	}
	if file.CertFile != "" || file.KeyFile != "" {
		private, readErr := protected(file.KeyFile)
		if readErr != nil {
			return Runtime{}, readErr
		}
		cert, readErr := os.ReadFile(file.CertFile)
		if readErr != nil {
			return Runtime{}, errors.New("cannot load client certificate")
		}
		pair, loadErr := tls.X509KeyPair(cert, private)
		if loadErr != nil {
			return Runtime{}, errors.New("invalid upstream identity")
		}
		tlsConfig.Certificates = []tls.Certificate{pair}
	}
	host, _, err := net.SplitHostPort(file.Listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return Runtime{}, errors.New("dashboard listener must use loopback")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = tlsConfig
	transport.TLSHandshakeTimeout = 5 * time.Second
	transport.ResponseHeaderTimeout = 10 * time.Second
	config := Config{Origin: file.Origin, Upstream: file.Upstream, APIKey: string(key), Development: file.Development, MaxSessions: file.MaxSessions}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	if _, err := New(config, client); err != nil {
		return Runtime{}, err
	}
	return Runtime{Config: config, ListenAddress: file.Listen, Assets: file.Assets, Client: client}, nil
}

func protected(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("protected file must be a private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open protected file")
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("protected file changed")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("protected file exceeds limit")
	}
	return raw, nil
}
