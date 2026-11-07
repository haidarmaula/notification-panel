package installation

import (
	"bytes"
	"crypto/ed25519"
	"maps"
	"net/netip"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/receipt"
)

type receiptConfig struct {
	Environment      string `json:"environment"`
	VerificationKeys []struct {
		ID  string `json:"id"`
		Key []byte `json:"key"`
	} `json:"verification_keys"`
	TrustedTLSProxies     []string `json:"trusted_tls_proxies"`
	IPPerMinute           int      `json:"ip_per_minute"`
	PrincipalPerMinute    int      `json:"principal_per_minute"`
	InstallationPerMinute int      `json:"installation_per_minute"`
}

// ReceiptPolicy contains one application's public trust and per-process ingress limits.
type ReceiptPolicy struct {
	Verifier              *receipt.Verifier
	IP, Principal         *receipt.RateWindow
	InstallationPerMinute int
	proxies               map[netip.Addr]bool
}

// TrustsProxy permits TLS and client address assertions only from an explicitly pinned peer.
func (p ReceiptPolicy) TrustsProxy(peer netip.Addr) bool { return p.proxies[peer.Unmap()] }

// ReceiptPolicies returns only projects explicitly enabled for receipt ingress.
func (r *Runtime) ReceiptPolicies() map[pgtype.UUID]ReceiptPolicy { return maps.Clone(r.receipts) }

func (c *receiptConfig) policy(mobileKey, originKey []byte) (ReceiptPolicy, error) {
	var p ReceiptPolicy
	if len(c.VerificationKeys) == 0 || len(c.VerificationKeys) > 16 || len(c.TrustedTLSProxies) > 16 {
		return p, ErrConfiguration
	}
	keys := make(map[string]ed25519.PublicKey, len(c.VerificationKeys))
	for _, entry := range c.VerificationKeys {
		if keys[entry.ID] != nil || bytes.Equal(entry.Key, mobileKey) || bytes.Equal(entry.Key, originKey) {
			return p, ErrConfiguration
		}
		keys[entry.ID] = entry.Key
	}
	var err error
	p.Verifier, err = receipt.NewVerifier(c.Environment, keys)
	if err != nil {
		return p, ErrConfiguration
	}
	p.IP, err = receipt.NewRateWindow(c.IPPerMinute)
	if err != nil {
		return p, ErrConfiguration
	}
	p.Principal, err = receipt.NewRateWindow(c.PrincipalPerMinute)
	if err != nil || c.InstallationPerMinute < 1 || c.InstallationPerMinute > 60000 {
		return p, ErrConfiguration
	}
	p.InstallationPerMinute = c.InstallationPerMinute
	p.proxies = make(map[netip.Addr]bool, len(c.TrustedTLSProxies))
	for _, value := range c.TrustedTLSProxies {
		addr, err := netip.ParseAddr(value)
		if err != nil || addr.IsUnspecified() || addr.IsMulticast() || addr.Zone() != "" || addr.Unmap().String() != value || p.proxies[addr] {
			return p, ErrConfiguration
		}
		p.proxies[addr] = true
	}
	return p, nil
}
