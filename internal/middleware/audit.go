// Package middleware provides composable HTTP middleware for the
// notification-panel API. Each middleware in this package follows the
// http.HandlerFunc decorator pattern and is safe for concurrent use.
//
// This file (audit.go) declares the AuditMiddleware which is responsible
// for extracting request-scoped attributes consumed by the audit
// subsystem: the client IP address and the raw User-Agent header.
package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// auditContextKey is the private type used for audit-related context keys.
//
// It exists to prevent collisions with other context-key namespaces. Go's
// context package deliberately treats keys as opaque; two keys with the same
// underlying string value but different static types are still distinct, so
// declaring a dedicated type here (as opposed to reusing a shared
// contextKey type) ensures that an audit key can never accidentally match a
// key from another middleware package. Do not merge this type with the
// contextKey type declared in jwt.go — the separation is the mechanism.
type auditContextKey string

// AuditIPKey is the context key under which the resolved client IP is stored.
//
// The value is a string set by AuditMiddleware.Use and read by the audit
// package's GetIP helper. It is exported because the audit package, which
// lives in a different import path, needs to reference it; callers outside
// of the audit subsystem should retrieve the value through that helper
// rather than reading the context directly.
const AuditIPKey auditContextKey = "audit_ip"

// AuditUserAgentKey is the context key under which the raw User-Agent header
// is stored.
//
// The value is a string set by AuditMiddleware.Use and read by the audit
// package's GetUserAgent helper. As with AuditIPKey, direct reads are
// discouraged outside the audit package.
const AuditUserAgentKey auditContextKey = "audit_user_agent"

// AuditMiddleware records request-scoped attributes needed by the audit
// subsystem: the client IP address and the raw User-Agent header.
//
// It performs no logging and makes no authorization decisions. Its sole
// purpose is to extract these two attributes from the incoming request and
// attach them to the request context so that downstream handlers — and
// ultimately the audit service — can read them without re-parsing the
// request. Placing this work in middleware keeps the extraction logic (in
// particular, the X-Forwarded-For handling) in one place instead of being
// duplicated in every audit call site.
//
// The middleware is stateless after construction apart from its IPHeader
// and TrustedProxies configuration fields and is safe for concurrent use.
type AuditMiddleware struct {
	// IPHeader is the name of the HTTP header from which the client IP is
	// read. It is exported so that deployments with a non-standard proxy
	// configuration can override the default without editing this package.
	// An empty value causes the middleware to skip header-based lookup and
	// fall back to RemoteAddr.
	IPHeader string

	// TrustedProxies is the set of CIDR ranges whose members are considered
	// trusted reverse proxies. The X-Forwarded-For header is honoured only
	// when the immediate peer (r.RemoteAddr) belongs to one of these
	// ranges. When the peer is not trusted, the header is ignored entirely
	// and the peer address itself is used — this closes the IP-spoofing
	// vector that exists when the service is reachable directly (see C5 in
	// code-review.md).
	//
	// A nil or empty slice means no peer is trusted, in which case the
	// header is never consulted and every request is attributed to its
	// immediate peer. Use DefaultTrustedProxies to obtain a sensible
	// default covering loopback and the RFC 1918 private ranges.
	//
	// Entries that are nil are skipped during iteration; callers are not
	// required to filter them.
	TrustedProxies []*net.IPNet
}

// NewAuditMiddleware constructs an AuditMiddleware configured to read the
// client IP from the X-Forwarded-For header and to trust only the loopback
// and RFC 1918 private address ranges as proxies.
//
// The default trust set is deliberately conservative: it covers the
// addresses typically used by sidecars, container bridges, and same-host
// proxies, but it does not extend to public ranges. A deployment that
// terminates at a public-IP proxy must override TrustedProxies after
// construction; a deployment that is exposed directly to the internet
// should leave TrustedProxies empty so the header is never consulted.
//
// The returned middleware is safe for concurrent use.
func NewAuditMiddleware() *AuditMiddleware {
	return &AuditMiddleware{
		IPHeader:       "X-Forwarded-For",
		TrustedProxies: DefaultTrustedProxies(),
	}
}

// DefaultTrustedProxies returns the default set of CIDR ranges whose
// members are considered trusted reverse proxies.
//
// The returned slice contains the IPv4 and IPv6 loopback ranges
// (127.0.0.0/8, ::1/128), the three RFC 1918 IPv4 private ranges
// (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16), and the IPv6 unique-local
// range (fc00::/7). These cover the addresses used by Docker bridge
// networks, Kubernetes pod networks, and same-host reverse proxies, which
// is the common deployment topology for this service.
//
// A fresh slice is returned on every call; callers may append to or
// otherwise mutate the result without affecting subsequent callers.
// Entries that fail to parse are silently skipped — with the hardcoded
// values above this never happens in practice, but the guard keeps the
// function total.
func DefaultTrustedProxies() []*net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",
		"::1/128",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"fc00::/7",
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, block, err := net.ParseCIDR(c)
		if err != nil {
			continue
		}
		out = append(out, block)
	}
	return out
}

// Use wraps next with audit-context injection.
//
// On every request the middleware resolves the client IP from the configured
// header (subject to the TrustedProxies check), reads the raw User-Agent
// string, and stores both on a copy of the request context. The augmented
// context is propagated to next via r.WithContext; the original request is
// not mutated.
//
// # Failure behaviour
//
// The middleware never rejects a request. If the IP cannot be determined,
// an empty string is recorded; if the User-Agent header is absent, an empty
// string is recorded. Downstream audit entries with empty values are
// legitimate — for example, when an action is triggered by an internal job
// rather than an HTTP request — and are not treated as errors.
//
// # Trust behaviour
//
// The X-Forwarded-For header is consulted only when the immediate peer
// (r.RemoteAddr) is itself a trusted proxy. In that case the header is
// walked from right to left, skipping any intermediate entries that are
// also trusted proxies, and the first untrusted address encountered is
// returned. When the peer is not trusted, the header is ignored entirely
// and the peer address itself is used — this prevents an attacker who can
// reach the service directly from forging the recorded IP.
//
// # Concurrency
//
// The returned handler is safe for concurrent use. It does not mutate the
// receiver and does not share mutable state across requests.
func (m *AuditMiddleware) Use(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractClientIP(r, m.IPHeader, m.TrustedProxies)
		ctx := context.WithValue(r.Context(), AuditIPKey, ip)
		ctx = context.WithValue(ctx, AuditUserAgentKey, r.UserAgent())
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

// extractClientIP resolves the client's IP address from the request,
// trusting the X-Forwarded-For header only when the immediate peer is a
// trusted proxy.
//
// # Trust model
//
// The function is deliberately conservative. It first parses the immediate
// peer address from r.RemoteAddr. If that address cannot be parsed, or if
// it does not fall within any of the supplied trusted CIDR ranges, the
// header is ignored and the peer address is returned verbatim. This means
// a deployment that is reachable directly — with no proxy in front — will
// never have its recorded IP influenced by a client-supplied header, which
// is the vulnerability described as C5 in code-review.md.
//
// When the peer is trusted, the header (if present and non-empty) is
// parsed as a standard X-Forwarded-For list of comma-separated addresses.
// The list is walked from right to left; entries that are themselves
// trusted proxies are skipped, and the first untrusted address encountered
// is returned as the client. This right-to-left walk is what makes the
// function robust against a client that prepends arbitrary addresses to
// the header: those entries are never reached unless every proxy between
// the client and the service has also been marked as trusted.
//
// Malformed entries within the header are skipped rather than aborting
// the walk, on the assumption that a partially-correct value is more
// useful for auditing than an empty one. If every entry in the header is
// either malformed or a trusted proxy, the function falls back to the
// peer address.
//
// # Fallback
//
// When the header is absent, unparseable, or untrusted, the function falls
// back to r.RemoteAddr, which for a Go HTTP server is a "host:port" string.
// The port is stripped with net.SplitHostPort; if parsing fails — which can
// happen for unusual listeners — the raw RemoteAddr is returned unmodified
// rather than discarded, on the assumption that a partially-correct value
// is more useful for debugging than an empty one.
//
// An empty header argument disables header lookup entirely; the peer
// address is always returned. This is the behaviour callers should rely on
// when TrustedProxies is empty or when header-based resolution has been
// deliberately disabled.
func extractClientIP(r *http.Request, header string, trusted []*net.IPNet) string {
	peerIP := peerIPFromRemoteAddr(r.RemoteAddr)

	// Only consult the header when the immediate peer is a trusted proxy.
	if peerIP == nil || !isTrustedProxy(peerIP, trusted) {
		if peerIP != nil {
			return peerIP.String()
		}
		// Could not parse RemoteAddr; return it as-is rather than
		// discarding a possibly-useful value.
		return r.RemoteAddr
	}

	if header == "" {
		return peerIP.String()
	}

	fwd := r.Header.Get(header)
	if fwd == "" {
		return peerIP.String()
	}

	// Walk the X-Forwarded-For chain from right to left, skipping entries
	// that are themselves trusted proxies. The first untrusted address
	// encountered is the closest-to-us client we can trust.
	parts := strings.Split(fwd, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		ip := net.ParseIP(candidate)
		if ip == nil {
			// Malformed entry; skip and continue the walk.
			continue
		}
		if isTrustedProxy(ip, trusted) {
			continue
		}
		return candidate
	}

	// Every entry in the chain was either malformed or a trusted proxy;
	// the only meaningful value left is the peer address itself.
	return peerIP.String()
}

// peerIPFromRemoteAddr parses the IP portion out of a Go HTTP server's
// RemoteAddr string, which is conventionally formatted as "host:port".
//
// If the string cannot be split with net.SplitHostPort — for example,
// because a custom listener supplied a bare address — the raw value is
// passed to net.ParseIP directly. The return value is nil when the string
// does not contain a recognisable IPv4 or IPv6 address in either form.
func peerIPFromRemoteAddr(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return net.ParseIP(host)
}

// isTrustedProxy reports whether ip falls within any of the supplied CIDR
// ranges.
//
// A nil ip never matches. Nil entries within trusted are skipped. An empty
// or nil trusted slice returns false for every input, which is the intended
// behaviour when no proxies are trusted.
func isTrustedProxy(ip net.IP, trusted []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, block := range trusted {
		if block != nil && block.Contains(ip) {
			return true
		}
	}
	return false
}
