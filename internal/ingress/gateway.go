// Package ingress verifies gateway identity and applies process-local request limits.
package ingress

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"hello/internal/deployment"
	"hello/internal/jsonobject"
	"hello/internal/ratelimit"
	"hello/pkg/response"
)

// Gateway accepts requests only from pinned, mutually authenticated reverse proxies.
type Gateway struct {
	Listener                 *deployment.PrivateListener
	peers                    map[netip.Addr]bool
	perIP, authIP, authTotal *ratelimit.Window
	active                   chan struct{}
}

// Parse requires explicit gateway addresses and per-replica request budgets.
func Parse(data json.RawMessage) (*Gateway, error) {
	var tls json.RawMessage
	var peers []string
	var perIP, authIP, authTotal, concurrent int
	if jsonobject.Decode(data, map[string]any{
		"endpoint": &tls, "gateway_ips": &peers, "ip_per_minute": &perIP,
		"auth_ip_per_minute": &authIP, "auth_total_per_minute": &authTotal, "concurrent_requests": &concurrent,
	}) != nil || len(peers) < 1 || len(peers) > 16 || concurrent < 1 || concurrent > 1024 || authIP > perIP || authIP > authTotal {
		return nil, deployment.ErrConfiguration
	}
	endpoint, err := deployment.ParsePrivateListener(tls)
	if err != nil {
		return nil, err
	}
	g := &Gateway{Listener: endpoint, peers: make(map[netip.Addr]bool, len(peers)), active: make(chan struct{}, concurrent)}
	for _, value := range peers {
		a, err := netip.ParseAddr(value)
		if err != nil || a.Zone() != "" || a.Unmap().String() != value || !(a.IsPrivate() || a.IsLoopback()) || g.peers[a] {
			return nil, deployment.ErrConfiguration
		}
		g.peers[a] = true
	}
	if g.perIP, err = ratelimit.New(perIP); err != nil {
		return nil, deployment.ErrConfiguration
	}
	if g.authIP, err = ratelimit.New(authIP); err != nil {
		return nil, deployment.ErrConfiguration
	}
	if g.authTotal, err = ratelimit.New(authTotal); err != nil {
		return nil, deployment.ErrConfiguration
	}
	return g, nil
}

// ClientIP accepts the single canonical address and HTTPS scheme asserted by a
// configured socket peer authenticated by the dedicated gateway client CA.
func (g *Gateway) ClientIP(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
		return ""
	}
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !g.peers[peer.Addr().Unmap()] {
		return ""
	}
	proto, forwarded := r.Header.Values("X-Forwarded-Proto"), r.Header.Values("X-Forwarded-For")
	if len(proto) != 1 || proto[0] != "https" || len(forwarded) != 1 {
		return ""
	}
	ip, err := netip.ParseAddr(forwarded[0])
	if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || ip.Unmap().String() != forwarded[0] {
		return ""
	}
	return ip.String()
}

// Protect rejects unauthorized traffic before it can reach a credential parser
// or acquire a database connection. Authentication paths have a separate budget.
func (g *Gateway) Protect(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ip := g.ClientIP(r)
		if ip == "" {
			response.JSON(w, http.StatusForbidden, nil, "secure gateway required")
			return
		}
		now := time.Now()
		if ok, delay := g.perIP.Allow(ip, now); !ok {
			limited(w, delay)
			return
		}
		_, pattern := mux.Handler(r)
		if strings.HasPrefix(pattern, "POST /api/v1/auth/") {
			if ok, delay := g.authIP.Allow(ip, now); !ok {
				limited(w, delay)
				return
			}
			if ok, delay := g.authTotal.Allow("auth", now); !ok {
				limited(w, delay)
				return
			}
		}
		select {
		case g.active <- struct{}{}:
			defer func() { <-g.active }()
			mux.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			response.JSON(w, http.StatusServiceUnavailable, nil, "service busy")
		}
	})
}

func limited(w http.ResponseWriter, delay int) {
	w.Header().Set("Retry-After", strconv.Itoa(delay))
	response.JSON(w, http.StatusTooManyRequests, nil, "request rate limit exceeded")
}
