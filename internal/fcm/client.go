package fcm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"cloud.google.com/go/auth"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/api/option"
	"hello/internal/pushpayload"
)

type routeKey struct {
	app                                   pgtype.UUID
	namespace, key, platform, addressType string
}

func keyFor(d pushpayload.Destination) routeKey {
	return routeKey{d.ApplicationID, d.Namespace, d.RouteKey, d.Platform, d.AddressType}
}

type clientState struct {
	mu                sync.Mutex
	closed            bool
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	closeOnce         sync.Once
	closeHTTP         func()
	sdk               *messaging.Client
	tokens            auth.TokenProvider
	routes            map[routeKey]*pushpayload.Builder
	config            Config
	sem               chan struct{}
	logical, physical pace
	breaker           breaker
	cooldown          time.Time
	target            string
}

// Client shares one project's budgets across declared application routes. Its
// trusted token provider must honor context and owns its own credential resources.
// Close joins active client work. Do not log the client or provider configuration.
type Client struct{ s *clientState }

func (c Client) String() string               { return "[REDACTED]" }
func (c Client) GoString() string             { return c.String() }
func (c Client) LogValue() slog.Value         { return slog.StringValue(c.String()) }
func (c Client) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

// New uses only the exact HTTPS project send endpoint, verified frozen roots,
// bounded connections/response sizes and explicit context-aware Bearer credentials.
// Construction makes no provider send; remote permissions/APNs are not proven.
func New(config Config, tokens auth.TokenProvider) (*Client, error) {
	base, err := verifiedHTTP(config)
	if err != nil {
		return nil, err
	}
	client, err := newClient(config, tokens, base, base.CloseIdleConnections)
	if err != nil {
		base.CloseIdleConnections()
	}
	return client, err
}

// verifiedHTTP owns frozen trust and never consults proxy configuration.
func verifiedHTTP(config Config) (*http.Transport, error) {
	if os.Getenv("SSL_CERT_FILE") != "" || os.Getenv("SSL_CERT_DIR") != "" {
		return nil, ErrConfiguration
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, ErrConfiguration
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	base := &http.Transport{
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  config.RequestTimeout,
		MaxResponseHeaderBytes: 16 * 1024,
		IdleConnTimeout:        time.Minute,
		MaxConnsPerHost:        config.Concurrency,
		MaxIdleConns:           config.Concurrency,
		MaxIdleConnsPerHost:    config.Concurrency,
		Protocols:              http1Protocols(),
	}
	return base, nil
}

func newClient(config Config, tokens auth.TokenProvider, base http.RoundTripper, closeHTTP func()) (*Client, error) {
	if config.Validate() != nil || tokens == nil || base == nil || closeHTTP == nil {
		return nil, ErrConfiguration
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &clientState{
		ctx:       ctx,
		cancel:    cancel,
		config:    config,
		tokens:    tokens,
		routes:    make(map[routeKey]*pushpayload.Builder),
		sem:       make(chan struct{}, config.Concurrency),
		closeHTTP: closeHTTP,
		target:    "https://fcm.googleapis.com/v1/projects/" + config.ProjectID + "/messages:send",
	}
	// Keep only validated builders, not caller-owned route slice storage.
	state.config.Routes = nil
	for _, route := range config.Routes {
		builder, err := pushpayload.New(route)
		key := routeKey{route.ApplicationID, route.Namespace, route.Key, route.Platform, route.AddressType}
		if err != nil || state.routes[key] != nil {
			cancel()
			return nil, ErrConfiguration
		}
		state.routes[key] = builder
	}
	interval := time.Second / time.Duration(config.RequestsPerSecond)
	state.logical.interval = interval
	state.physical.interval = interval
	state.breaker.threshold = config.BreakerThreshold
	state.breaker.cooldown = config.BreakerCooldown
	httpClient := &http.Client{
		Transport:     &guardTransport{client: state, base: base},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errTransport },
	}
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: config.ProjectID}, option.WithHTTPClient(httpClient))
	if err != nil {
		cancel()
		return nil, ErrConfiguration
	}
	state.sdk, err = app.Messaging(ctx)
	if err != nil {
		cancel()
		return nil, ErrConfiguration
	}
	return &Client{s: state}, nil
}

func (c *clientState) begin() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.wg.Add(1)
	return true
}

func (c *clientState) linked(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(parent, c.config.RequestTimeout)
	joined := make(chan struct{})
	stop := context.AfterFunc(c.ctx, func() { cancel(); close(joined) })
	return ctx, func() {
		cancel()
		if !stop() {
			<-joined
		}
	}
}

// Close revokes unused permits, cancels/joins active work and closes idle HTTP
// resources. Unused permits need not return to make progress; they cannot send.
func (c *Client) Close() {
	if c == nil || c.s == nil {
		return
	}
	s := c.s
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.cancel()
		s.mu.Unlock()
		s.wg.Wait()
		s.closeHTTP()
	})
}

// BreakerOpen reports whether the local circuit awaits a successful probe.
func (c *Client) BreakerOpen() bool {
	b := &c.s.breaker
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.until.IsZero()
}

// QuotaCooldown reports whether the local client is honoring a provider quota hold.
func (c *Client) QuotaCooldown() bool {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	return time.Now().Before(c.s.cooldown)
}

func (s *clientState) deferPermit(reason DeferralReason, at time.Time) error {
	if s.config.Observer != nil {
		s.config.Observer.PermitDeferred(reason)
	}
	return &Deferred{RetryAt: at}
}

type permitState struct {
	client *clientState
	token  breakerToken
	used   atomic.Bool
}

// Permit is a one-use pre-claim concurrency/rate/breaker admission. Copies share
// state. A DB claim failure must Release it; only the later executor authorizes I/O.
type Permit struct{ state *permitState }

// Acquire returns a scheduling deferral before a DB attempt is charged. Capacity
// is bounded without an internal waiting queue; normal pacing is cancellable.
func (c *Client) Acquire(parent context.Context) (*Permit, error) {
	if c == nil || c.s == nil {
		return nil, ErrConfiguration
	}
	s := c.s
	if !s.begin() {
		return nil, ErrClosed
	}
	defer s.wg.Done()
	ctx, stop := s.linked(parent)
	defer stop()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	cooldown := s.cooldown
	s.mu.Unlock()
	if time.Now().Before(cooldown) {
		return nil, s.deferPermit(DeferralQuota, cooldown)
	}
	select {
	case s.sem <- struct{}{}:
	default:
		return nil, s.deferPermit(DeferralConcurrency, time.Now().Add(s.logical.interval))
	}
	token, until, ok := s.breaker.acquire(time.Now())
	if !ok {
		<-s.sem
		return nil, s.deferPermit(DeferralBreaker, until)
	}
	if err := s.logical.wait(ctx); err != nil {
		s.breaker.finish(token, Outcome{}, time.Now())
		<-s.sem
		return nil, err
	}
	s.mu.Lock()
	closed := s.closed
	cooldown = s.cooldown
	s.mu.Unlock()
	if closed || ctx.Err() != nil {
		s.breaker.finish(token, Outcome{}, time.Now())
		<-s.sem
		if closed {
			return nil, ErrClosed
		}
		return nil, ctx.Err()
	}
	if time.Now().Before(cooldown) {
		s.breaker.finish(token, Outcome{}, time.Now())
		<-s.sem
		return nil, s.deferPermit(DeferralQuota, cooldown)
	}
	return &Permit{state: &permitState{client: s, token: token}}, nil
}

// Release is neutral to the breaker and idempotent across copied permits.
func (p *Permit) Release() {
	if p == nil || p.state == nil || !p.state.used.CompareAndSwap(false, true) {
		return
	}
	p.state.client.breaker.finish(p.state.token, Outcome{}, time.Now())
	<-p.state.client.sem
}

// Send consumes the permit once and builds only its trusted scoped route. The
// DB executor uses SendAuthorized with a valid single-use attempt proof.
// Raw SDK errors, access tokens, addresses and receipt values never enter Outcome.
func (p *Permit) Send(parent context.Context, request pushpayload.Request) Outcome {
	return p.send(parent, request, nil)
}

// Allows reports whether an explicit route belongs to this open client. It is
// advisory; SendAuthorized and the payload builder still validate the request.
func (c *Client) Allows(route pushpayload.Route) bool {
	if c == nil || c.s == nil || route.Environment != c.s.config.Environment {
		return false
	}
	s := c.s
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.routes[routeKey{route.ApplicationID, route.Namespace, route.Key, route.Platform, route.AddressType}] != nil
}

// SendAuthorized consumes a permit from this client and requires a final DB
// authorization callback at the physical boundary. The trusted callback releases
// all locks before returning a conservative local lease/deadline cutoff.
func (c *Client) SendAuthorized(ctx context.Context, p *Permit, request pushpayload.Request, check func(context.Context) (time.Time, error)) Outcome {
	if c == nil || c.s == nil || p == nil || p.state == nil || p.state.client != c.s || check == nil {
		return Outcome{Disposition: NotSubmitted, Reason: AuthorizationFailure}
	}
	return p.send(ctx, request, check)
}

func (p *Permit) send(parent context.Context, request pushpayload.Request, check func(context.Context) (time.Time, error)) (out Outcome) {
	if p == nil || p.state == nil || !p.state.used.CompareAndSwap(false, true) {
		return Outcome{Disposition: NotSubmitted, Reason: PermitUsed}
	}
	s := p.state.client
	if observer := s.config.Observer; observer != nil {
		start := time.Now()
		defer func() { observer.LogicalResult(out.Disposition, out.Reason, time.Since(start)) }()
	}
	defer func() {
		s.breaker.finish(p.state.token, out, time.Now())
		<-s.sem
	}()
	if !s.begin() {
		return Outcome{Disposition: NotSubmitted, Reason: Closed}
	}
	defer s.wg.Done()
	ctx, stop := s.linked(parent)
	defer stop()
	if ctx.Err() != nil {
		return Outcome{Disposition: NotSubmitted, Reason: Cancelled}
	}
	builder := s.routes[keyFor(request.Destination)]
	if builder == nil {
		return Outcome{Disposition: NotSubmitted, Reason: InvalidPayload}
	}
	_, err := builder.Build(time.Now(), request)
	if err != nil {
		return Outcome{Disposition: NotSubmitted, Reason: InvalidPayload}
	}
	ctx, deadlineCancel := context.WithDeadline(ctx, request.Deadline)
	defer deadlineCancel()
	token, err := s.tokens.Token(ctx)
	if ctx.Err() != nil {
		return Outcome{Disposition: NotSubmitted, Reason: Cancelled}
	}
	if err != nil || token == nil || !token.Expiry.After(time.Now().Add(s.config.RequestTimeout)) ||
		(token.Type != "" && token.Type != "Bearer") || !opaqueToken(token.Value) {
		return Outcome{Disposition: NotSubmitted, Reason: CredentialFailure}
	}
	// Credential refresh/permit delay must not extend freshness or send stale work.
	message, err := builder.Build(time.Now(), request)
	if err != nil {
		return Outcome{Disposition: NotSubmitted, Reason: InvalidPayload}
	}
	call, cancel := context.WithCancel(ctx)
	defer cancel()
	record := &callRecord{cancel: cancel, token: token.Value, deadline: request.Deadline, check: check}
	name, sdkErr := s.sdk.Send(context.WithValue(call, callKey{}, record), message)
	snapshot := record.snapshot()
	out = classify(name, sdkErr, snapshot, s.config.ProjectID, request.Deadline)
	if out.PhysicalRequests == 0 && ctx.Err() != nil {
		out.Reason = Cancelled
	}
	if out.Reason == Quota {
		// Project-wide provider advice must survive this delivery's expiry.
		hold := retryAt(snapshot.retryAfter, snapshot.at, time.Minute, time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC))
		s.mu.Lock()
		if hold.After(s.cooldown) {
			s.cooldown = hold
		}
		s.mu.Unlock()
	}
	return out
}

func opaqueToken(value string) bool {
	return len(value) > 0 && len(value) <= 16*1024 && !strings.ContainsFunc(value, func(r rune) bool {
		return r > 127 || unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

func http1Protocols() *http.Protocols {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	return p
}
