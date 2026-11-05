package fcm

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"cloud.google.com/go/auth"
	"hello/internal/deployment"
	"hello/internal/jsonobject"
)

const oauthEndpoint = "https://oauth2.googleapis.com/token"
const messagingScope = "https://www.googleapis.com/auth/firebase.messaging"

// ErrCredentials contains no provider error, token, assertion or key material.
var ErrCredentials = errors.New("FCM credentials unavailable")

// FileCredentialPolicy pins a managed secret's expected identity. Cross-project
// grants require explicit approval; neither JSON nor this flag proves remote IAM.
type FileCredentialPolicy struct {
	Path                 string
	CredentialProjectID  string
	ServiceAccountEmail  string
	CrossProjectApproved bool
}

// NewFile constructs a client from a protected service-account key. It performs
// local validation only, owns credential HTTP resources and never discovers ADC.
// Close cancels and joins refresh work as part of the client's active sends.
func NewFile(ctx context.Context, config Config, policy FileCredentialPolicy) (*Client, error) {
	if config.validate() != nil || ctx.Err() != nil {
		return nil, ErrConfiguration
	}
	data, err := deployment.ReadSecret(policy.Path)
	if err != nil || ctx.Err() != nil {
		return nil, ErrConfiguration
	}
	// The parser copies the needed strings; the input is not retained by the client.
	source, closeSource, err := fileSource(config, policy, data, nil)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		closeSource()
		return nil, ErrConfiguration
	}
	client, err := New(config, source)
	if err != nil {
		closeSource()
		return nil, err
	}
	closeFCM := client.s.closeHTTP
	client.s.closeHTTP = func() { closeFCM(); closeSource() }
	if ctx.Err() != nil {
		client.Close()
		return nil, ErrConfiguration
	}
	return client, nil
}

type serviceAccount struct {
	Type, ProjectID, PrivateKeyID, PrivateKey, ClientEmail, TokenURI      string
	AuthURI, ClientID, AuthProviderCertURL, ClientCertURL, UniverseDomain string
}

func parseServiceAccount(data []byte, target string, policy FileCredentialPolicy) (serviceAccount, error) {
	var key serviceAccount
	if len(data) > 64*1024 || !utf8.Valid(data) || !projectLabel(target) || !projectLabel(policy.CredentialProjectID) ||
		policy.ServiceAccountEmail == "" || (target != policy.CredentialProjectID && !policy.CrossProjectApproved) {
		return key, ErrConfiguration
	}
	fields := map[string]any{
		"type": &key.Type, "project_id": &key.ProjectID, "private_key_id": &key.PrivateKeyID,
		"private_key": &key.PrivateKey, "client_email": &key.ClientEmail, "token_uri": &key.TokenURI,
		"auth_uri": &key.AuthURI, "client_id": &key.ClientID, "auth_provider_x509_cert_url": &key.AuthProviderCertURL,
		"client_x509_cert_url": &key.ClientCertURL, "universe_domain": &key.UniverseDomain,
	}
	var present map[string]json.RawMessage
	if json.Unmarshal(data, &present) != nil {
		return serviceAccount{}, ErrConfiguration
	}
	for _, name := range []string{"type", "project_id", "private_key_id", "private_key", "client_email", "token_uri"} {
		if _, ok := present[name]; !ok {
			return serviceAccount{}, ErrConfiguration
		}
	}
	selected := make(map[string]any, len(present))
	for name := range present {
		field, ok := fields[name]
		if !ok {
			return serviceAccount{}, ErrConfiguration
		}
		selected[name] = field
	}
	// Decode rechecks duplicates, exact case, nulls and trailing objects. The first
	// map decode selects optional fields only; it does not authorize the document.
	if jsonobject.Decode(data, selected) != nil || key.Type != "service_account" || key.ProjectID != policy.CredentialProjectID ||
		key.ClientEmail != policy.ServiceAccountEmail || !serviceEmail(key.ClientEmail, key.ProjectID) ||
		!opaqueToken(key.PrivateKeyID) || len(key.PrivateKeyID) > 255 || key.TokenURI != oauthEndpoint ||
		(key.UniverseDomain != "" && key.UniverseDomain != "googleapis.com") || !validRSAKey(key.PrivateKey) {
		return serviceAccount{}, ErrConfiguration
	}
	return key, nil
}

func serviceEmail(email, project string) bool {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || len(local) < 1 || len(local) > 64 || domain != project+".iam.gserviceaccount.com" {
		return false
	}
	for _, b := range []byte(local) {
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-') {
			return false
		}
	}
	return true
}

func validRSAKey(value string) bool {
	block, rest := pem.Decode([]byte(value))
	if block == nil || len(block.Bytes) > 4096 || len(block.Headers) != 0 ||
		!bytes.HasPrefix(bytes.TrimSpace([]byte(value)), []byte("-----BEGIN "+block.Type+"-----")) ||
		len(bytes.TrimSpace(rest)) != 0 {
		return false
	}
	var key *rsa.PrivateKey
	var err error
	switch block.Type {
	case "PRIVATE KEY":
		parsed, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes)
		if parseErr != nil {
			return false
		}
		key, _ = parsed.(*rsa.PrivateKey)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	default:
		return false
	}
	return err == nil && key != nil && key.N.BitLen() >= 2048 && key.N.BitLen() <= 4096 &&
		len(key.Primes) == 2 && key.Validate() == nil
}

type fileTokenSource struct {
	gate     chan struct{}
	provider auth.TokenProvider
	cached   *auth.Token
	timeout  time.Duration
}

func fileSource(config Config, policy FileCredentialPolicy, data []byte, testTransport http.RoundTripper) (*fileTokenSource, func(), error) {
	key, err := parseServiceAccount(data, config.ProjectID, policy)
	if config.validate() != nil || err != nil {
		return nil, nil, ErrConfiguration
	}
	var closeHTTP func()
	if testTransport == nil {
		base, err := verifiedHTTP(config)
		if err != nil {
			return nil, nil, err
		}
		testTransport, closeHTTP = base, base.CloseIdleConnections
	} else {
		closeHTTP = func() {}
	}
	client := &http.Client{
		Transport:     oauthTransport{base: testTransport},
		Timeout:       config.RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrCredentials },
	}
	provider, err := auth.New2LOTokenProvider(&auth.Options2LO{
		Email:        key.ClientEmail,
		PrivateKey:   []byte(key.PrivateKey),
		PrivateKeyID: key.PrivateKeyID,
		TokenURL:     oauthEndpoint,
		Scopes:       []string{messagingScope},
		Client:       client,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		closeHTTP()
		return nil, nil, ErrConfiguration
	}
	return &fileTokenSource{gate: make(chan struct{}, 1), provider: provider, timeout: config.RequestTimeout}, closeHTTP, nil
}

func (s *fileTokenSource) Token(parent context.Context) (*auth.Token, error) {
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ErrCredentials
	}
	defer func() { <-s.gate }()
	if ctx.Err() != nil {
		return nil, ErrCredentials
	}
	if s.cached != nil && time.Until(s.cached.Expiry) > time.Minute {
		return copyAccessToken(s.cached), nil
	}
	token, err := s.provider.Token(ctx)
	if err != nil || ctx.Err() != nil || token == nil || token.Type != "Bearer" ||
		!opaqueToken(token.Value) || !token.Expiry.After(time.Now().Add(s.timeout)) {
		return nil, ErrCredentials
	}
	s.cached = copyAccessToken(token)
	return copyAccessToken(s.cached), nil
}

func copyAccessToken(token *auth.Token) *auth.Token {
	return &auth.Token{Value: token.Value, Type: token.Type, Expiry: token.Expiry}
}

// oauthTransport is separate from FCM quota accounting and accepts one fixed
// library endpoint. It validates bounded expiry before SDK duration arithmetic.
type oauthTransport struct{ base http.RoundTripper }

func (t oauthTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost || r.URL.String() != oauthEndpoint {
		return nil, ErrCredentials
	}
	cloned := r.Clone(r.Context())
	cloned.GetBody = nil
	response, err := t.base.RoundTrip(cloned)
	if err != nil || response == nil || response.Body == nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, ErrCredentials
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 16*1024+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(data) > 16*1024 {
		return nil, ErrCredentials
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		var body struct {
			AccessToken string `json:"access_token"`
			Type        string `json:"token_type"`
			Expires     int64  `json:"expires_in"`
			IDToken     string `json:"id_token"`
		}
		if json.Unmarshal(data, &body) != nil || body.Type != "Bearer" || !opaqueToken(body.AccessToken) ||
			body.Expires < 1 || body.Expires > 3600 || body.IDToken != "" {
			return nil, ErrCredentials
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}
