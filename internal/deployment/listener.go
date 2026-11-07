package deployment

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/netip"

	"hello/internal/jsonobject"
)

// PrivateListener binds an explicit loopback or private IP and requires a trusted TLS 1.3 client certificate.
type PrivateListener struct {
	address string
	tls     *tls.Config
}

// ParsePrivateListener loads an endpoint and its purpose-specific trust bundle from protected JSON.
func ParsePrivateListener(data json.RawMessage) (*PrivateListener, error) {
	var addr, cert, key, ca string
	if jsonobject.Decode(data, map[string]any{"listen": &addr, "server_certificate_pem": &cert, "server_key_pem": &key, "client_ca_pem": &ca}) != nil {
		return nil, ErrConfiguration
	}
	ip, err := netip.ParseAddrPort(addr)
	if err != nil || ip.Port() == 0 || ip.Addr().Zone() != "" || !(ip.Addr().IsLoopback() || ip.Addr().IsPrivate()) {
		return nil, ErrConfiguration
	}
	pair, err := tls.X509KeyPair([]byte(cert), []byte(key))
	if err != nil {
		return nil, ErrConfiguration
	}
	roots, err := CertificatePool(ca)
	if err != nil {
		return nil, ErrConfiguration
	}
	return &PrivateListener{address: ip.String(), tls: &tls.Config{MinVersion: tls.VersionTLS13,
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, Certificates: []tls.Certificate{pair}}}, nil
}

// Listen opens an authenticated TLS listener owned by the caller.
func (a *PrivateListener) Listen(ctx context.Context) (net.Listener, error) {
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", a.address)
	if err != nil {
		return nil, errors.New("private TLS listener unavailable")
	}
	return tls.NewListener(l, a.tls), nil
}
