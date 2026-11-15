package provisioning

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"hello/internal/database/sqlc"
	"hello/internal/deployment"
	"hello/internal/jsonobject"
)

var errProvisionerPolicy = errors.New("invalid provisioner policy")
var errProvisionerUnavailable = errors.New("provisioner unavailable")

// Policy is immutable deployment configuration for the protected provisioner.
type Policy struct {
	listenAddress string
	identity      string
	database      *pgxpool.Config
	tls           *tls.Config
	store         *FileStore
}

// LoadPolicy parses a bounded exact-schema deployment policy and validates all
// network trust and protected storage inputs before the process starts.
func LoadPolicy(data []byte) (*Policy, error) {
	if !deployment.EnvironmentAllowed() || len(data) == 0 || len(data) > 64*1024 {
		return nil, errProvisionerPolicy
	}
	bounded, err := jsonobject.Read(bytes.NewReader(data))
	if err != nil || !jsonobject.UniqueLowercaseFields(bounded) {
		return nil, errProvisionerPolicy
	}
	var address, credentialRoot, databaseURL, identity, caPEM, certificatePEM, privateKeyPEM string
	if jsonobject.Decode(bounded, map[string]any{
		"listen_address": &address, "credential_root": &credentialRoot, "database_url": &databaseURL,
		"ingress_identity": &identity, "ca_pem": &caPEM,
		"server_certificate_pem": &certificatePEM, "server_private_key_pem": &privateKeyPEM,
	}) != nil {
		return nil, errProvisionerPolicy
	}
	listenAddress, err := privateListenAddress(address)
	if err != nil {
		return nil, errProvisionerPolicy
	}
	if os.Geteuid() != 0 || runtime.GOOS != "linux" {
		return nil, errProvisionerPolicy
	}
	store, err := NewFileStore(credentialRoot)
	if err != nil {
		return nil, errProvisionerPolicy
	}
	if !validIngressIdentity(identity) {
		return nil, errProvisionerPolicy
	}
	databaseConfig, err := deployment.Database(databaseURL, "hello-provisioner")
	if err != nil || deployment.CheckDatabaseFiles(databaseURL) != nil {
		return nil, errProvisionerPolicy
	}
	databaseConfig.MaxConns = 1
	databaseConfig.MinConns = 0
	databaseConfig.ConnConfig.RuntimeParams["statement_timeout"] = "2000"
	databaseConfig.ConnConfig.RuntimeParams["lock_timeout"] = "500"
	databaseConfig.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "2000"
	databaseConfig.ConnConfig.RuntimeParams["search_path"] = "pg_catalog,pg_temp"
	roots, err := deployment.CertificatePool(caPEM)
	if err != nil {
		return nil, errProvisionerPolicy
	}
	certificate, err := tls.X509KeyPair([]byte(certificatePEM), []byte(privateKeyPEM))
	if err != nil || len(certificate.Certificate) == 0 {
		return nil, errProvisionerPolicy
	}
	return &Policy{
		listenAddress: listenAddress,
		identity:      identity,
		database:      databaseConfig,
		tls: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    roots,
			Certificates: []tls.Certificate{certificate},
		},
		store: store,
	}, nil
}

// Run opens the process-owned database pool, then serves the single stage
// route over verified TLS until ctx is canceled or startup/runtime fails.
func (p *Policy) Run(ctx context.Context) error {
	if p == nil || p.database == nil || p.tls == nil || p.store == nil || ctx == nil || ctx.Err() != nil {
		return errProvisionerUnavailable
	}
	startup, cancelStartup := context.WithTimeout(ctx, 10*time.Second)
	pool, err := pgxpool.NewWithConfig(startup, p.database.Copy())
	if err != nil {
		cancelStartup()
		return errProvisionerUnavailable
	}
	if err := pool.Ping(startup); err != nil {
		cancelStartup()
		pool.Close()
		return errProvisionerUnavailable
	}
	queries := sqlc.New(pool)
	verifier, err := NewDBReservationVerifier(queries)
	if err != nil {
		cancelStartup()
		pool.Close()
		return errProvisionerUnavailable
	}
	service, err := NewService(p.identity, verifier, p.store)
	if err != nil {
		cancelStartup()
		pool.Close()
		return errProvisionerUnavailable
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(startup, "tcp", p.listenAddress)
	if err != nil {
		cancelStartup()
		pool.Close()
		return errProvisionerUnavailable
	}
	cancelStartup()
	defer pool.Close()
	tlsListener := tls.NewListener(listener, p.tls.Clone())
	server := &http.Server{
		Handler:           service,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    32 * 1024,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(tlsListener) }()
	select {
	case err := <-serveDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errProvisionerUnavailable
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errProvisionerUnavailable
		}
		return nil
	}
}

func privateListenAddress(value string) (string, error) {
	address, err := netip.ParseAddrPort(value)
	if err != nil || address.Port() == 0 || address.String() != value || address.Addr().Zone() != "" ||
		address.Addr().IsUnspecified() || address.Addr().IsMulticast() ||
		!(address.Addr().IsLoopback() || address.Addr().IsPrivate()) {
		return "", errProvisionerPolicy
	}
	return address.String(), nil
}

func validIngressIdentity(value string) bool {
	uri, err := url.Parse(value)
	return err == nil && uri.IsAbs() && uri.Host != "" && uri.User == nil && uri.RawQuery == "" &&
		uri.Fragment == "" && uri.String() == value && !strings.ContainsAny(value, "\r\n\x00")
}
