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
	"strings"
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
	server := &http.Server{
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second,
		MaxHeaderBytes: 16 * 1024, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
			if r.Method == http.MethodPost && r.TLS != nil && r.TLS.ServerName == r.Host && r.URL.RawQuery == "" {
				switch {
				case r.Host == "oauth2.googleapis.com" && r.URL.Path == "/token" && validAssertion(r, public):
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"token_type":"Bearer","access_token":"` + fixtureBearer + `","expires_in":3600}`))
					log.Print("accepted synthetic OAuth request")
					return
				case r.Host == "fcm.googleapis.com" && r.URL.Path == "/v1/projects/integration-fixture/messages:send" && validSend(r):
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

func validSend(r *http.Request) bool {
	var body struct {
		Message struct {
			Token   string
			Data    map[string]string
			Android struct{ TTL string }
		}
	}
	if r.Header.Get("Authorization") != "Bearer "+fixtureBearer || json.NewDecoder(r.Body).Decode(&body) != nil {
		return false
	}
	m := body.Message
	ttl, err := time.ParseDuration(m.Android.TTL)
	return err == nil && ttl > 0 && ttl <= 5*time.Minute &&
		(m.Token == "execution-address-0" || m.Token == "execution-address-1") &&
		m.Data["fixture"] == "integration" && m.Data["schema_version"] == "1" &&
		m.Data["application_id"] != "" && m.Data["notification_id"] != "" && m.Data["delivery_id"] != "" &&
		m.Data["registration_id"] != "" && strings.HasPrefix(m.Data["receipt_token"], "r1.fixture-key.")
}
