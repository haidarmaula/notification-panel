// Package dashboard provides the browser session boundary for the staff dashboard.
package dashboard

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sessionLifetime = 12 * time.Hour
const anonymousLifetime = 5 * time.Minute
const responseLimit = 2 << 20

type credentials struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
}
type session struct {
	mu            sync.Mutex
	csrf          string
	expires       time.Time
	tokens        credentials
	closed        bool
	authenticated bool
}

// Config is immutable after New. Development permits HTTP only on a loopback origin.
type Config struct {
	Origin           string
	Upstream         string
	APIKey           string
	Development      bool
	MaxSessions      int
	VerifiedClientIP func(*http.Request) string
}

// Server owns a bounded single-replica session store; restarting requires login.
type Server struct {
	config     Config
	client     *http.Client
	mu         sync.Mutex
	sessions   map[string]*session
	slots      chan struct{}
	cookieName string
}

// New constructs a fixed-upstream BFF with redirect rejection and bounded I/O.
func New(config Config, client *http.Client) (*Server, error) {
	origin, err := url.Parse(config.Origin)
	if err != nil || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, errors.New("invalid dashboard origin")
	}
	if config.Development && (origin.Scheme != "http" || (origin.Hostname() != "127.0.0.1" && origin.Hostname() != "localhost")) {
		return nil, errors.New("development requires loopback HTTP")
	}
	if origin.Scheme != "https" && !(config.Development && origin.Scheme == "http" && (origin.Hostname() == "127.0.0.1" || origin.Hostname() == "localhost")) {
		return nil, errors.New("dashboard requires HTTPS")
	}
	upstream, err := url.Parse(config.Upstream)
	if err != nil || upstream.Scheme != "https" || upstream.Host == "" || upstream.User != nil || upstream.RawQuery != "" || upstream.Fragment != "" || upstream.Path != "" || config.APIKey == "" {
		return nil, errors.New("invalid dashboard upstream")
	}
	if client == nil {
		return nil, errors.New("upstream client is required")
	}
	copied := *client
	copied.Timeout = 10 * time.Second
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if config.MaxSessions == 0 {
		config.MaxSessions = 1000
	}
	if config.MaxSessions < 1 || config.MaxSessions > 10000 {
		return nil, errors.New("invalid session capacity")
	}
	name := "__Host-hello_session"
	if config.Development {
		name = "hello_dev_session"
	}
	return &Server{config: config, client: &copied, sessions: make(map[string]*session), slots: make(chan struct{}, 64), cookieName: name}, nil
}

func opaque() string {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic("secure random source unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(data[:])
}
func reply(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func fail(w http.ResponseWriter, status int, message string) {
	reply(w, status, map[string]any{"success": false, "message": message})
}
func (s *Server) cookie(w http.ResponseWriter, id string, age int) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName, Value: id, Path: "/", HttpOnly: true, Secure: !s.config.Development, SameSite: http.SameSiteStrictMode, MaxAge: age})
}
func (s *Server) insert(tokens credentials) (string, *session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, value := range s.sessions {
		if now.After(value.expires) {
			delete(s.sessions, key)
		}
	}
	authenticated := tokens.Access != ""
	count := 0
	oldestID := ""
	var oldest time.Time
	for key, value := range s.sessions {
		if value.authenticated == authenticated {
			count++
			if !authenticated && (oldestID == "" || value.expires.Before(oldest)) {
				oldestID = key
				oldest = value.expires
			}
		}
	}
	if count >= s.config.MaxSessions {
		if authenticated {
			return "", nil, false
		}
		delete(s.sessions, oldestID)
	}
	lifetime := sessionLifetime
	if !authenticated {
		lifetime = anonymousLifetime
	}
	id := opaque()
	value := &session{csrf: opaque(), expires: now.Add(lifetime), tokens: tokens, authenticated: authenticated}
	s.sessions[id] = value
	return id, value, true
}
func (s *Server) lookup(r *http.Request) (string, *session) {
	cookies := r.CookiesNamed(s.cookieName)
	if len(cookies) != 1 {
		return "", nil
	}
	id := cookies[0].Value
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.sessions[id]
	if value != nil && time.Now().Before(value.expires) {
		return id, value
	}
	delete(s.sessions, id)
	return "", nil
}
func (s *Server) discard(id string, value *session) {
	value.closed = true
	value.tokens = credentials{}
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// ServeHTTP accepts only dashboard session routes and the explicit business allowlist.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		fail(w, 503, "service unavailable")
		return
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != s.config.Origin) {
		fail(w, 403, "request origin rejected")
		return
	}
	if r.URL.RawQuery != "" && (r.URL.Path == "/bff/session" || r.URL.Path == "/bff/login" || r.URL.Path == "/bff/logout") {
		fail(w, 400, "invalid query")
		return
	}
	id, value := s.lookup(r)
	if r.URL.Path == "/bff/session" && r.Method == http.MethodGet {
		if value == nil {
			var ok bool
			id, value, ok = s.insert(credentials{})
			if !ok {
				fail(w, 503, "service unavailable")
				return
			}
			s.cookie(w, id, int(anonymousLifetime.Seconds()))
		}
		value.mu.Lock()
		defer value.mu.Unlock()
		if value.closed {
			fail(w, 401, "session expired")
			return
		}
		reply(w, 200, map[string]any{"authenticated": value.tokens.Access != "", "csrfToken": value.csrf})
		return
	}
	if value == nil {
		fail(w, 401, "session expired")
		return
	}
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.closed || time.Now().After(value.expires) {
		fail(w, 401, "session expired")
		return
	}
	if r.Method != http.MethodGet {
		if r.Header.Get("Origin") != s.config.Origin || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(value.csrf)) != 1 {
			fail(w, 403, "request verification failed")
			return
		}
	}
	switch {
	case r.URL.Path == "/bff/login" && r.Method == http.MethodPost:
		s.login(w, r, id, value)
	case r.URL.Path == "/bff/logout" && r.Method == http.MethodPost:
		tokens := value.tokens
		s.discard(id, value)
		s.cookie(w, "", -1)
		if tokens.Refresh != "" {
			body, _ := json.Marshal(map[string]string{"refresh_token": tokens.Refresh})
			_, _, _ = s.call(r, "POST", "/api/v1/auth/logout", "", body, "")
		}
		reply(w, 200, map[string]bool{"success": true})
	default:
		if value.tokens.Access == "" {
			fail(w, 401, "session expired")
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/bff/api")
		if !strings.HasPrefix(r.URL.Path, "/bff/api/") || r.URL.EscapedPath() != r.URL.Path || !allowed(r.Method, path) {
			fail(w, 404, "not found")
			return
		}
		s.forward(w, r, id, value, path)
	}
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Method == http.MethodGet {
		return nil, true
	}
	if r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity" {
		fail(w, 415, "unsupported media type")
		return nil, false
	}
	if r.Method != http.MethodDelete && !strings.HasSuffix(r.URL.Path, "/send") {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			fail(w, 415, "unsupported media type")
			return nil, false
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
	if err != nil {
		fail(w, 413, "request body too large")
		return nil, false
	}
	return body, true
}
func (s *Server) login(w http.ResponseWriter, r *http.Request, id string, value *session) {
	if value.tokens.Access != "" {
		fail(w, 409, "already signed in")
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || input.Email == "" || input.Password == "" || len(input.Password) > 72 {
		fail(w, 400, "invalid sign-in request")
		return
	}
	status, raw, err := s.call(r, "POST", "/api/v1/auth/login", "", body, "")
	if err != nil || status >= 500 || status < 200 || status >= 300 && status != 401 {
		fail(w, 502, "sign-in service unavailable")
		return
	}
	if status == 401 {
		fail(w, 401, "invalid credentials")
		return
	}
	tokens, err := decodeTokens(raw)
	if err != nil {
		fail(w, 502, "sign-in service unavailable")
		return
	}
	// Remove the anonymous identity before allocating its authenticated successor.
	s.discard(id, value)
	next, created, ok := s.insert(tokens)
	if !ok {
		logout, _ := json.Marshal(map[string]string{"refresh_token": tokens.Refresh})
		_, _, _ = s.call(r, "POST", "/api/v1/auth/logout", "", logout, "")
		fail(w, 503, "service unavailable")
		return
	}
	s.cookie(w, next, int(sessionLifetime.Seconds()))
	reply(w, 200, map[string]any{"authenticated": true, "csrfToken": created.csrf})
}
func decodeTokens(raw []byte) (credentials, error) {
	var out struct {
		Data credentials `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Data.Access == "" || out.Data.Refresh == "" {
		return credentials{}, errors.New("invalid credential response")
	}
	return out.Data, nil
}
func (s *Server) call(r *http.Request, method, path, query string, body []byte, token string) (int, []byte, error) {
	status, raw, _, err := s.callWithHeaders(r, method, path, query, body, token)
	return status, raw, err
}

func (s *Server) callWithHeaders(r *http.Request, method, path, query string, body []byte, token string) (int, []byte, http.Header, error) {
	target := s.config.Upstream + path
	if query != "" {
		target += "?" + query
	}
	request, err := http.NewRequestWithContext(r.Context(), method, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	if s.config.VerifiedClientIP != nil {
		ip := s.config.VerifiedClientIP(r)
		if ip == "" {
			return 0, nil, nil, errors.New("verified gateway client required")
		}
		request.Header.Set("X-Forwarded-For", ip)
		request.Header.Set("X-Forwarded-Proto", "https")
	}
	request.Header.Set("X-API-Key", s.config.APIKey)
	request.Header.Set("Accept", "application/json")
	if body != nil && len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if conditionalRoute(method, strings.TrimPrefix(path, "/api/v1")) {
		for _, name := range []string{"If-Match", "If-None-Match"} {
			if value := r.Header.Get(name); value != "" {
				request.Header.Set(name, value)
			}
		}
	}
	if key := r.Header.Get("Idempotency-Key"); key != "" && strings.HasSuffix(path, "/send") {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return 0, nil, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if len(raw) > responseLimit {
		return 0, nil, nil, errors.New("upstream response too large")
	}
	return response.StatusCode, raw, response.Header.Clone(), err
}
func (s *Server) forward(w http.ResponseWriter, r *http.Request, id string, value *session, path string) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	status, raw, headers, err := s.callWithHeaders(r, r.Method, "/api/v1"+path, r.URL.RawQuery, body, value.tokens.Access)
	if err == nil && isSelfPasswordFailure(path, status, raw) {
		fail(w, http.StatusUnprocessableEntity, "current password is incorrect")
		return
	}
	if err == nil && status == 401 {
		refresh, _ := json.Marshal(map[string]string{"refresh_token": value.tokens.Refresh})
		code, pair, refreshErr := s.call(r, "POST", "/api/v1/auth/refresh", "", refresh, "")
		if refreshErr != nil || code != 200 {
			s.discard(id, value)
			s.cookie(w, "", -1)
			fail(w, 401, "session expired")
			return
		}
		tokens, decodeErr := decodeTokens(pair)
		if decodeErr != nil {
			s.discard(id, value)
			s.cookie(w, "", -1)
			fail(w, 401, "session expired")
			return
		}
		value.tokens = tokens
		status, raw, headers, err = s.callWithHeaders(r, r.Method, "/api/v1"+path, r.URL.RawQuery, body, tokens.Access)
		if err == nil && isSelfPasswordFailure(path, status, raw) {
			fail(w, http.StatusUnprocessableEntity, "current password is incorrect")
			return
		}
	}
	if err != nil {
		fail(w, 502, "request outcome unavailable")
		return
	}
	if status == 401 {
		s.discard(id, value)
		s.cookie(w, "", -1)
	}
	if status == http.StatusServiceUnavailable && campaignAcceptancePath(path) {
		w.Header().Set("Retry-After", "5")
		fail(w, http.StatusServiceUnavailable, "campaign acceptance is held")
		return
	}
	if status >= 500 || status < 200 || status >= 300 && status < 400 {
		fail(w, 502, "upstream service unavailable")
		return
	}
	if etag := headers.Get("ETag"); etag != "" && isRegistryPath(path) {
		w.Header().Set("ETag", etag)
	}
	if path == "/profile/password" && r.Method == http.MethodPatch && status >= 200 && status < 300 {
		s.discard(id, value)
		s.cookie(w, "", -1)
	}
	if status == 204 {
		w.WriteHeader(status)
		return
	}
	if !json.Valid(raw) {
		fail(w, 502, "invalid upstream response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

var appPath = regexp.MustCompile(`^/apps/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}(/.*)?$`)
var itemPath = regexp.MustCompile(`^/(campaigns|users|segments|templates)/[1-9][0-9]*(/(statistics|deliveries|reads|send|schedule))?$`)
var audiencePath = regexp.MustCompile(`^/(users/[1-9][0-9]*/devices|segments/[1-9][0-9]*/members)(/[1-9][0-9]*)?$`)
var staffDetailPath = regexp.MustCompile(`^/staff/([1-9][0-9]*)(/(status|password))?$`)
var registryAppPath = regexp.MustCompile(`^/admin/apps/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})(/staff-grants(?:/([1-9][0-9]*))?)?$`)
var scopedReadPath = regexp.MustCompile(`^/(uploads(?:/([1-9][0-9]*)/rows)?|audit-logs)$`)

func positiveInt64(value string) bool {
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0
}

func isRegistryPath(path string) bool {
	return path == "/admin/apps" || registryAppPath.MatchString(path)
}

func conditionalRoute(method, path string) bool {
	if method == http.MethodPatch && registryAppPath.MatchString(path) {
		match := registryAppPath.FindStringSubmatch(path)
		return match[2] == ""
	}
	if method != http.MethodPut && method != http.MethodDelete {
		return false
	}
	match := registryAppPath.FindStringSubmatch(path)
	return match != nil && match[2] != "" && match[3] != ""
}

func isSelfPasswordFailure(path string, status int, raw []byte) bool {
	if path != "/profile/password" || status != http.StatusUnauthorized {
		return false
	}
	var response struct {
		Message string `json:"message"`
	}
	return json.Unmarshal(raw, &response) == nil && response.Message == "current password is incorrect"
}

func campaignAcceptancePath(path string) bool {
	app := appPath.FindStringSubmatch(path)
	if app == nil {
		return false
	}
	item := itemPath.FindStringSubmatch(app[1])
	return item != nil && item[1] == "campaigns" && (item[3] == "send" || item[3] == "schedule")
}

func allowed(method, path string) bool {
	if path == "/profile/password" {
		return method == http.MethodPatch
	}
	if path == "/staff" {
		return method == http.MethodGet || method == http.MethodPost
	}
	if match := staffDetailPath.FindStringSubmatch(path); match != nil && positiveInt64(match[1]) {
		if match[2] == "" {
			return method == http.MethodGet || method == http.MethodPatch || method == http.MethodDelete
		}
		return method == http.MethodPatch
	}
	if path == "/admin/apps" {
		return method == http.MethodGet || method == http.MethodPost
	}
	if match := registryAppPath.FindStringSubmatch(path); match != nil {
		if match[2] == "" {
			return method == http.MethodGet || method == http.MethodPatch
		}
		if match[3] == "" {
			return method == http.MethodGet
		}
		return positiveInt64(match[3]) && (method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete)
	}
	if method == http.MethodGet && strings.HasPrefix(path, "/apps/") {
		match := appPath.FindStringSubmatch(path)
		if match != nil {
			readMatch := scopedReadPath.FindStringSubmatch(match[1])
			if readMatch != nil && (readMatch[2] == "" || positiveInt64(readMatch[2])) {
				return true
			}
		}
	}
	if method == "PATCH" && path == "/profile" {
		return true
	}
	if method == "GET" && (path == "/apps" || path == "/profile") {
		return true
	}
	match := appPath.FindStringSubmatch(path)
	if match == nil {
		return false
	}
	suffix := match[1]
	if audience := audiencePath.FindStringSubmatch(suffix); audience != nil {
		if strings.HasPrefix(audience[1], "users/") {
			return method == "GET" && audience[2] == ""
		}
		return method == "GET" && audience[2] == "" || (method == "PUT" || method == "DELETE") && audience[2] != ""
	}
	if method == "GET" && (suffix == "" || suffix == "/campaigns" || suffix == "/users" || suffix == "/segments" || suffix == "/templates") {
		return true
	}
	if method == "POST" && (suffix == "/campaigns" || suffix == "/templates" || suffix == "/segments") {
		return true
	}
	item := itemPath.FindStringSubmatch(suffix)
	if item == nil {
		return false
	}
	if method == "GET" {
		return item[3] == "" || item[1] == "campaigns" && (item[3] == "statistics" || item[3] == "deliveries" || item[3] == "reads")
	}
	if item[1] != "campaigns" {
		return item[3] == "" && (method == "PUT" && (item[1] == "templates" || item[1] == "segments") || method == "DELETE" && item[1] == "segments")
	}
	return (method == "PUT" || method == "DELETE") && item[3] == "" || method == "POST" && (item[3] == "send" || item[3] == "schedule")
}
