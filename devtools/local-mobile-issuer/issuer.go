package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"hello/internal/jsonobject"
)

const (
	mobileTokenLifetime = 10 * time.Minute
	originTokenLifetime = 90 * time.Second
	pairingLifetime     = 10 * time.Minute
	maxPairingAttempts  = 5
	maxConfigBytes      = 16 * 1024
	maxRequestBytes     = 4 * 1024
	maxOriginAssertions = 5
	originPurpose       = "mobile_installation_enrollment"
)

type issuerConfig struct {
	AppID            string `json:"app_id"`
	Issuer           string `json:"issuer"`
	Audience         string `json:"audience"`
	Purpose          string `json:"purpose"`
	Namespace        string `json:"namespace"`
	Subject          string `json:"subject"`
	MobilePrivateKey []byte `json:"mobile_private_key"`
	OriginIssuer     string `json:"origin_issuer"`
	OriginAudience   string `json:"origin_audience"`
	OriginPrivateKey []byte `json:"origin_private_key"`
}

type projectPolicy struct {
	AppID           string `json:"app_id"`
	Issuer          string `json:"issuer"`
	Audience        string `json:"audience"`
	Purpose         string `json:"purpose"`
	Namespace       string `json:"namespace"`
	VerificationKey []byte `json:"verification_key"`
	OriginIssuer    string `json:"origin_issuer"`
	OriginAudience  string `json:"origin_audience"`
	OriginKey       []byte `json:"origin_key"`
	Routes          []struct {
		Platform          string `json:"platform"`
		AddressType       string `json:"address_type"`
		ProviderNamespace string `json:"provider_namespace"`
		ProviderRouteKey  string `json:"provider_route_key"`
	} `json:"routes"`
}

type mobilePolicyFile struct {
	CurrentKeyVersion int32  `json:"current_key_version"`
	FingerprintKey    []byte `json:"fingerprint_key"`
	EncryptionKeys    []struct {
		Version int32  `json:"version"`
		Key     []byte `json:"key"`
	} `json:"encryption_keys"`
	Projects []projectPolicy `json:"projects"`
}

type mobileClaims struct {
	Purpose string `json:"purpose"`
	jwt.RegisteredClaims
}

type originClaims struct {
	AppID            string `json:"app_id"`
	Namespace        string `json:"namespace"`
	CredentialDigest string `json:"credential_digest"`
	Purpose          string `json:"purpose"`
	jwt.RegisteredClaims
}

type apiEnvelope struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

type sessionResponse struct {
	Credential string `json:"credential"`
}

type originResponse struct {
	Origin string `json:"origin"`
}

type sessionRequest struct {
	AppID       string `json:"app_id"`
	PairingCode string `json:"pairing_code"`
}

type originRequest struct {
	AppID string `json:"app_id"`
}

type issuerServer struct {
	mu             sync.Mutex
	cfg            issuerConfig
	pairingCode    string
	pairingExpires time.Time
	failedAttempts int
	pairingUsed    bool
	credential     string
	credentialExp  time.Time
	originCount    int
}

func createFiles(directory, appID, namespace, subject, providerNamespace, providerRouteKey string) error {
	directory, err := absoluteDirectory(directory)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return err
	}
	created := true
	defer func() {
		if created {
			_ = os.RemoveAll(directory)
		}
	}()

	_, mobilePrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	_, originPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	fingerprintKey := make([]byte, 32)
	if _, err := rand.Read(fingerprintKey); err != nil {
		return err
	}
	encryptionKey := make([]byte, 32)
	if _, err := rand.Read(encryptionKey); err != nil {
		return err
	}
	cfg := issuerConfig{
		AppID: appID, Issuer: "hello-local-mobile-test", Audience: "hello-local-mobile-client",
		Purpose: "hello_mobile_installation", Namespace: namespace, Subject: subject,
		MobilePrivateKey: mobilePrivate, OriginIssuer: "hello-local-origin-test",
		OriginAudience: "hello-local-origin-client", OriginPrivateKey: originPrivate,
	}
	policy := mobilePolicyFile{CurrentKeyVersion: 1, FingerprintKey: fingerprintKey}
	policy.EncryptionKeys = append(policy.EncryptionKeys, struct {
		Version int32  `json:"version"`
		Key     []byte `json:"key"`
	}{Version: 1, Key: encryptionKey})
	project := projectPolicy{
		AppID: cfg.AppID, Issuer: cfg.Issuer, Audience: cfg.Audience, Purpose: cfg.Purpose,
		Namespace: cfg.Namespace, VerificationKey: mobilePrivate.Public().(ed25519.PublicKey),
		OriginIssuer: cfg.OriginIssuer, OriginAudience: cfg.OriginAudience,
		OriginKey: originPrivate.Public().(ed25519.PublicKey),
	}
	project.Routes = append(project.Routes, struct {
		Platform          string `json:"platform"`
		AddressType       string `json:"address_type"`
		ProviderNamespace string `json:"provider_namespace"`
		ProviderRouteKey  string `json:"provider_route_key"`
	}{Platform: "ANDROID", AddressType: "FCM_FID", ProviderNamespace: providerNamespace, ProviderRouteKey: providerRouteKey})
	policy.Projects = []projectPolicy{project}

	privateData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	policyData, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	if err := writePrivateFile(filepath.Join(directory, "issuer.json"), privateData); err != nil {
		return err
	}
	if err := writePrivateFile(filepath.Join(directory, "mobile-policy.json"), policyData); err != nil {
		return err
	}
	created = false
	return nil
}

func writePrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	writeErr := error(nil)
	if _, err = file.Write(data); err != nil {
		writeErr = err
	}
	if err == nil {
		writeErr = file.Sync()
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	return writeErr
}

func readConfig(path string) (issuerConfig, error) {
	var result issuerConfig
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return result, errors.New("invalid config path")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() <= 0 || info.Size() > maxConfigBytes {
		return result, errors.New("unsafe config file")
	}
	data, err := os.ReadFile(path)
	if err != nil || !jsonobject.UniqueLowercaseFields(data) {
		return result, errors.New("invalid config data")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return issuerConfig{}, errors.New("invalid config data")
	}
	if err := decoder.Decode(new(any)); err != io.EOF || !validConfig(result) {
		return issuerConfig{}, errors.New("invalid config data")
	}
	return result, nil
}

func validConfig(cfg issuerConfig) bool {
	return validUUID(cfg.AppID) && validIdentityText(cfg.Issuer) && validIdentityText(cfg.Audience) &&
		validIdentityText(cfg.Purpose) && validIdentityText(cfg.Namespace) && validIdentityText(cfg.Subject) &&
		validIdentityText(cfg.OriginIssuer) && validIdentityText(cfg.OriginAudience) &&
		len(cfg.MobilePrivateKey) == ed25519.PrivateKeySize && len(cfg.OriginPrivateKey) == ed25519.PrivateKeySize &&
		validPrivateKey(cfg.MobilePrivateKey) && validPrivateKey(cfg.OriginPrivateKey) &&
		!bytes.Equal(cfg.MobilePrivateKey[ed25519.SeedSize:], cfg.OriginPrivateKey[ed25519.SeedSize:])
}

func validPrivateKey(key []byte) bool {
	derived := ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])
	return bytes.Equal(derived, key)
}

func validUUID(value string) bool {
	if len(value) != 36 || strings.ToLower(value) != value {
		return false
	}
	for i, char := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func validIdentityText(value string) bool {
	return value != "" && len(value) <= 255 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func loopbackAddress(value string) bool {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		return false
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

func runServer(cfg issuerConfig, address string) error {
	codeBytes := make([]byte, 32)
	if _, err := rand.Read(codeBytes); err != nil {
		return err
	}
	code := base64.RawURLEncoding.EncodeToString(codeBytes)
	api := &issuerServer{cfg: cfg, pairingCode: code, pairingExpires: time.Now().Add(pairingLifetime)}
	server := &http.Server{
		Addr: address, Handler: api.handler(), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 * 1024,
		ErrorLog: log.New(io.Discard, "", 0),
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(os.Stdout, "Local issuer ready at http://%s\nOne-use pairing code (expires in 10 minutes): %s\n", address, code); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Serve(listener) }()
	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}

func (s *issuerServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", s.handleSession)
	mux.HandleFunc("POST /v1/origin", s.handleOrigin)
	return mux
}

func (s *issuerServer) handleSession(w http.ResponseWriter, r *http.Request) {
	var input sessionRequest
	if !decodeRequest(w, r, &input, "app_id", "pairing_code") {
		writeJSON(w, http.StatusBadRequest, apiEnvelope{Success: false, Message: "invalid request"})
		return
	}
	if input.AppID != s.cfg.AppID {
		writeJSON(w, http.StatusUnauthorized, apiEnvelope{Success: false, Message: "invalid pairing code"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pairingUsed || !time.Now().Before(s.pairingExpires) || s.failedAttempts >= maxPairingAttempts ||
		subtle.ConstantTimeCompare([]byte(input.PairingCode), []byte(s.pairingCode)) != 1 {
		s.failedAttempts++
		writeJSON(w, http.StatusUnauthorized, apiEnvelope{Success: false, Message: "invalid pairing code"})
		return
	}
	now := time.Now().UTC()
	credential, expires, err := s.mobileCredential(now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiEnvelope{Success: false, Message: "issuer unavailable"})
		return
	}
	s.credential = credential
	s.credentialExp = expires
	s.pairingUsed = true
	writeJSON(w, http.StatusOK, apiEnvelope{Success: true, Data: sessionResponse{Credential: credential}})
}

func (s *issuerServer) handleOrigin(w http.ResponseWriter, r *http.Request) {
	var input originRequest
	if !decodeRequest(w, r, &input, "app_id") {
		writeJSON(w, http.StatusBadRequest, apiEnvelope{Success: false, Message: "invalid request"})
		return
	}
	bearer := bearerToken(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.AppID != s.cfg.AppID || bearer == "" || s.credential == "" || s.originCount >= maxOriginAssertions ||
		!time.Now().Before(s.credentialExp) || subtle.ConstantTimeCompare([]byte(bearer), []byte(s.credential)) != 1 {
		writeJSON(w, http.StatusUnauthorized, apiEnvelope{Success: false, Message: "invalid mobile credential"})
		return
	}
	origin, err := s.originAssertion(bearer, time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiEnvelope{Success: false, Message: "issuer unavailable"})
		return
	}
	s.originCount++
	writeJSON(w, http.StatusOK, apiEnvelope{Success: true, Data: originResponse{Origin: origin}})
}

func (s *issuerServer) mobileCredential(now time.Time) (string, time.Time, error) {
	id, err := randomID()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := now.Add(mobileTokenLifetime)
	claims := mobileClaims{Purpose: s.cfg.Purpose, RegisteredClaims: jwt.RegisteredClaims{
		Issuer: s.cfg.Issuer, Subject: s.cfg.Subject, Audience: jwt.ClaimStrings{s.cfg.Audience},
		ExpiresAt: jwt.NewNumericDate(expires), IssuedAt: jwt.NewNumericDate(now), ID: id,
	}}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(ed25519.PrivateKey(s.cfg.MobilePrivateKey))
	return signed, expires, err
}

func (s *issuerServer) originAssertion(credential string, now time.Time) (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(credential))
	claims := originClaims{
		AppID: s.cfg.AppID, Namespace: s.cfg.Namespace,
		CredentialDigest: base64.RawURLEncoding.EncodeToString(digest[:]), Purpose: originPurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: s.cfg.OriginIssuer, Subject: s.cfg.Subject, Audience: jwt.ClaimStrings{s.cfg.OriginAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(originTokenLifetime)), IssuedAt: jwt.NewNumericDate(now), ID: id,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return token.SignedString(ed25519.PrivateKey(s.cfg.OriginPrivateKey))
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any, fields ...string) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil || !jsonobject.UniqueLowercaseFields(data) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		return false
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(data, &members) != nil || len(members) != len(fields) {
		return false
	}
	for _, name := range fields {
		if _, ok := members[name]; !ok {
			return false
		}
	}
	return true
}

func bearerToken(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return ""
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func writeJSON(w http.ResponseWriter, status int, body apiEnvelope) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
