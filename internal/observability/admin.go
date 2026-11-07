package observability

import (
	"context"
	"crypto/tls"

	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"time"

	"hello/internal/deployment"
	"hello/internal/jsonobject"
)

var errAdmin = errors.New("invalid private diagnostics configuration")

// Admin binds one explicitly configured management address and requires mutual TLS.
type Admin struct {
	address string
	tls     *tls.Config
}

// ParseAdmin loads all TLS material from the protected role policy.
func ParseAdmin(data json.RawMessage) (*Admin, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var addr, cert, key, ca string
	if jsonobject.Decode(data, map[string]any{"listen": &addr, "server_certificate_pem": &cert, "server_key_pem": &key, "client_ca_pem": &ca}) != nil {
		return nil, errAdmin
	}
	ip, err := netip.ParseAddrPort(addr)
	if err != nil || ip.Port() == 0 || ip.Addr().Zone() != "" || !(ip.Addr().IsLoopback() || ip.Addr().IsPrivate()) {
		return nil, errAdmin
	}
	pair, err := tls.X509KeyPair([]byte(cert), []byte(key))
	if err != nil {
		return nil, errAdmin
	}
	roots, err := deployment.CertificatePool(ca)
	if err != nil {
		return nil, errAdmin
	}
	return &Admin{address: ip.String(), tls: &tls.Config{MinVersion: tls.VersionTLS13,
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, Certificates: []tls.Certificate{pair}}}, nil
}

func (a *Admin) listen(ctx context.Context) (net.Listener, error) {
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", a.address)
	if err != nil {
		return nil, errors.New("private diagnostics listener unavailable")
	}
	return tls.NewListener(l, a.tls), nil
}

func servePrivate(ctx context.Context, l net.Listener, h http.Handler) error {
	server := &http.Server{Handler: h, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 5 * time.Second,
		IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	joined := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = server.Close(); close(joined) })
	err := server.Serve(l)
	if !stop() {
		<-joined
	}
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return errors.New("private diagnostics listener stopped")
}
