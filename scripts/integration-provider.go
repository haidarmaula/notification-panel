//go:build ignore

// Integration-provider serves the two fixed Google hosts inside the isolated
// Docker network. All identities, addresses, keys and responses are synthetic.
package main

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const fixtureIssuer = "fixture@integration-fixture.iam.gserviceaccount.com"
const fixtureBearer = "disposable-worker-integration-access-token"

func main() {
	data, err := os.ReadFile("/fixtures/oauth-public.pem")
	if err != nil {
		log.Fatal("missing synthetic account")
	}
	block, _ := pem.Decode(data)
	if block == nil {
		log.Fatal("invalid synthetic account")
	}
	public, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		log.Fatal("invalid synthetic account")
	}
	cert, err := tls.LoadX509KeyPair("/fixtures/provider.crt", "/fixtures/provider.key")
	if err != nil {
		log.Fatal("invalid fixture TLS identity")
	}
	listener, err := net.Listen("tcp", ":443")
	if err != nil {
		log.Fatal("failed to bind fixture")
	}
	load := os.Getenv("HELLO_PROVIDER_LOAD") == "synthetic"
	stats := &loadStats{seen: make(map[string]time.Time)}
	server := &http.Server{
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second,
		MaxHeaderBytes: 16 * 1024, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
			if load && r.Method == http.MethodGet && r.TLS != nil && r.Host == "fcm.googleapis.com" && r.TLS.ServerName == r.Host && r.URL.Path == "/__load/stats" {
				stats.mu.Lock()
				counts := stats.loadCounts
				stats.mu.Unlock()
				_ = json.NewEncoder(w).Encode(counts)
				return
			}
			if r.Method == http.MethodPost && r.TLS != nil && r.TLS.ServerName == r.Host && r.URL.RawQuery == "" {
				switch {
				case r.Host == "oauth2.googleapis.com" && r.URL.Path == "/token" && validAssertion(r, public):
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"token_type":"Bearer","access_token":"` + fixtureBearer + `","expires_in":3600}`))
					log.Print("accepted synthetic OAuth request")
					return
				case r.Host == "fcm.googleapis.com" && r.URL.Path == "/v1/projects/integration-fixture/messages:send":
					if load {
						stats.respond(w, r)
						return
					}
					if _, _, _, valid := readSend(r, false); !valid {
						break
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"name":"projects/integration-fixture/messages/accepted-1"}`))
					log.Print("accepted synthetic FCM request")
					return
				}
			}
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
		}),
	}
	if err := os.WriteFile("/tmp/provider-ready", []byte("ready"), 0600); err != nil {
		log.Fatal("failed to mark fixture ready")
	}
	if err := server.ServeTLS(listener, "", ""); err != nil {
		log.Fatal("fixture stopped")
	}
}

func validAssertion(r *http.Request, public *rsa.PublicKey) bool {
	if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		return false
	}
	var claims struct {
		jwt.RegisteredClaims
		Scope string `json:"scope"`
	}
	token, err := jwt.ParseWithClaims(r.PostForm.Get("assertion"), &claims, func(*jwt.Token) (any, error) { return public, nil },
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(fixtureIssuer),
		jwt.WithAudience("https://oauth2.googleapis.com/token"), jwt.WithExpirationRequired())
	return err == nil && token.Valid && token.Header["kid"] == "worker-fixture" &&
		claims.Scope == "https://www.googleapis.com/auth/firebase.messaging"
}

func readSend(r *http.Request, load bool) (token, delivery, mode string, valid bool) {
	var body struct {
		Message struct {
			Token   string
			Data    map[string]string
			Android struct{ TTL string }
		}
	}
	if r.Header.Get("Authorization") != "Bearer "+fixtureBearer || json.NewDecoder(r.Body).Decode(&body) != nil {
		return "", "", "", false
	}
	m := body.Message
	ttl, err := time.ParseDuration(m.Android.TTL)
	fixture := ttl <= 5*time.Minute && (m.Token == "execution-address-0" || m.Token == "execution-address-1") && m.Data["fixture"] == "integration"
	if load {
		n, numErr := strconv.Atoi(strings.TrimPrefix(m.Token, "load-"))
		fixture = numErr == nil && m.Token == "load-"+strconv.Itoa(n) && n >= 1 && n <= 50000 &&
			m.Data["fixture"] == "worker-load" && (m.Data["load_mode"] == "healthy" || m.Data["load_mode"] == "transient" || m.Data["load_mode"] == "quota") && ttl <= time.Hour
	}
	return m.Token, m.Data["delivery_id"], m.Data["load_mode"], fixture && err == nil && ttl > 0 && m.Data["schema_version"] == "1" &&
		m.Data["application_id"] != "" && m.Data["notification_id"] != "" && m.Data["delivery_id"] != "" &&
		m.Data["registration_id"] != "" && strings.HasPrefix(m.Data["receipt_token"], "r1.fixture-key.")
}

// Only the internal load fixture enables these aggregate counters. Arrival times
// cover a sliding second; IDs are synthetic and bounded by the runner's input.
type loadStats struct {
	loadCounts
	mu          sync.Mutex
	seen        map[string]time.Time
	arrivals    []time.Time
	first       time.Time
	quotaIssued bool
}

type loadCounts struct {
	LastResponseUTC                                     time.Time
	Active, Requests, Accepted, Failed, Invalid, Unique int
	PeakInflight, PeakSecond                            int
	ArrivalSpanMS, ShortestRetryMS                      int64
}

func (s *loadStats) respond(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	now := time.Now()
	if s.first.IsZero() {
		s.first = now
	}
	s.ArrivalSpanMS = now.Sub(s.first).Milliseconds()
	s.Active++
	s.Requests++
	s.PeakInflight = max(s.Active, s.PeakInflight)
	for len(s.arrivals) > 0 && now.Sub(s.arrivals[0]) >= time.Second {
		s.arrivals = s.arrivals[1:]
	}
	s.arrivals = append(s.arrivals, now)
	s.PeakSecond = max(len(s.arrivals), s.PeakSecond)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.Active--
		s.LastResponseUTC = time.Now().UTC()
		s.mu.Unlock()
	}()
	token, delivery, mode, valid := readSend(r, true)
	if !valid {
		s.mu.Lock()
		s.Invalid++
		s.mu.Unlock()
		http.Error(w, "invalid synthetic load request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	first, repeat := s.seen[delivery]
	if !repeat {
		s.seen[delivery] = now
		s.Unique++
	} else {
		ms := now.Sub(first).Milliseconds()
		if s.ShortestRetryMS == 0 || ms < s.ShortestRetryMS {
			s.ShortestRetryMS = ms
		}
	}
	// Inject one project quota response across all waves, or one transient
	// response per campaign for its deterministically selected registration.
	quota := mode == "quota" && !s.quotaIssued
	if quota {
		s.quotaIssued = true
	}
	reject := quota || mode == "transient" && token == "load-1" && !repeat
	s.mu.Unlock()
	// A controlled service time exposes true overlap at the private HTTP server.
	timer := time.NewTimer(15 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-r.Context().Done():
		return
	case <-timer.C:
	}
	w.Header().Set("Content-Type", "application/json")
	if reject {
		s.mu.Lock()
		s.Failed++
		s.mu.Unlock()
		if quota {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"synthetic quota"}}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":503,"status":"UNAVAILABLE","message":"synthetic transient","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNAVAILABLE"}]}}`))
		return
	}
	s.mu.Lock()
	s.Accepted++
	s.mu.Unlock()
	_, _ = w.Write([]byte(`{"name":"projects/integration-fixture/messages/accepted-` + delivery + `"}`))
}
