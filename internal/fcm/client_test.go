package fcm

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/auth"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/installation"
	"hello/internal/pushpayload"
	"hello/internal/receipt"
)

type tokenFunc func(context.Context) (*auth.Token, error)

func (f tokenFunc) Token(ctx context.Context) (*auth.Token, error) { return f(ctx) }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func transportFixture(t *testing.T) (Config, pushpayload.Request) {
	t.Helper()
	id := func(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }
	now := time.Now()
	scope := receipt.Scope{ApplicationID: id(1), OriginEpoch: id(2), NotificationID: 3, DeliveryID: id(4), BindingID: 5, InstallationID: id(6), OwnerEpoch: 7, ExpiresUnix: now.Add(24 * time.Hour).Unix()}
	signer, err := receipt.NewSigner("fixture", map[string]ed25519.PrivateKey{"k": ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := signer.Sign("k", scope)
	if err != nil {
		t.Fatal(err)
	}
	route := pushpayload.Route{ApplicationID: scope.ApplicationID, Environment: "fixture", Namespace: "firebase-fixture", Key: "route", Platform: "ANDROID", AddressType: installation.FCMRegistrationToken}
	config := Config{ProjectID: "fixture-project", Environment: "fixture", Routes: []pushpayload.Route{route}, RequestsPerSecond: 1000, Concurrency: 4, RequestTimeout: 10 * time.Second, BreakerThreshold: 5, BreakerCooldown: 30 * time.Second}
	request := pushpayload.Request{Scope: scope, Capability: capability, Destination: pushpayload.Destination{ApplicationID: scope.ApplicationID, BindingID: scope.BindingID, InstallationID: scope.InstallationID, OwnerEpoch: scope.OwnerEpoch, Namespace: route.Namespace, RouteKey: route.Key, Platform: route.Platform, AddressType: route.AddressType, Address: "fixture-accepted"}, Deadline: now.Add(time.Hour), Title: "Fixture", Body: "Body", Data: map[string]string{"fixture": "integration"}}
	return config, request
}
func fixtureToken(context.Context) (*auth.Token, error) {
	return &auth.Token{Value: "synthetic_access_token", Expiry: time.Now().Add(time.Hour)}, nil
}
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func makeClient(t *testing.T, config Config, rt http.RoundTripper) *Client {
	t.Helper()
	c, err := newClient(config, tokenFunc(fixtureToken), rt, func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}
func send(t *testing.T, c *Client, r pushpayload.Request) Outcome {
	t.Helper()
	permit, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return permit.Send(context.Background(), r)
}
func TestSDKSinglePhysicalAttemptAndSafeClassification(t *testing.T) {
	for _, tt := range []struct {
		name        string
		status      int
		body        string
		reason      Reason
		disposition Disposition
		retry       time.Duration
	}{
		{"accepted", 200, `{"name":"projects/fixture-project/messages/accepted"}`, Success, Accepted, 0},
		{"unregistered", 404, `{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`, Unregistered, Rejected, 0},
		{"generic 404", 404, `{"error":{"code":404,"status":"NOT_FOUND"}}`, CredentialFailure, Rejected, 0},
		{"payload", 400, `{"error":{"code":400,"status":"INVALID_ARGUMENT"}}`, InvalidPayload, Rejected, 0},
		{"sender", 403, `{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"SENDER_ID_MISMATCH"}]}}`, SenderMismatch, Rejected, 0},
		{"apns auth", 401, `{"error":{"code":401,"status":"UNAUTHENTICATED","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"THIRD_PARTY_AUTH_ERROR"}]}}`, APNsCredentials, Rejected, 0},
		{"quota", 429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`, Quota, Rejected, time.Minute},
		{"503", 503, `{"error":{"code":503,"status":"UNAVAILABLE"}}`, Unavailable, Rejected, 10 * time.Second},
		{"500", 500, `{"error":{"code":500,"status":"INTERNAL"}}`, Internal, Rejected, 10 * time.Second},
		{"empty success", 200, `{}`, InvalidResponse, Unconfirmed, 10 * time.Second},
		{"foreign name", 200, `{"name":"projects/other/messages/id"}`, InvalidResponse, Unconfirmed, 10 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config, request := transportFixture(t)
			calls := 0
			c := makeClient(t, config, roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != cTarget(config) || r.Header.Get("Authorization") != "Bearer synthetic_access_token" || r.GetBody != nil {
					t.Error("wrong transport boundary")
				}
				return response(tt.status, tt.body), nil
			}))
			started := time.Now()
			out := send(t, c, request)
			if out.Reason != tt.reason || out.Disposition != tt.disposition || out.PhysicalRequests != 1 || calls != 1 {
				t.Fatalf("unexpected safe outcome: %+v calls=%d", out, calls)
			}
			if time.Since(started) > time.Second {
				t.Fatal("hidden SDK backoff was not interrupted")
			}
			if tt.retry > 0 && out.RetryAt.Before(started.Add(tt.retry)) {
				t.Fatal("retry floor shortened")
			}
			if strings.Contains(fmt.Sprint(out), "synthetic_access_token") {
				t.Fatal("credential in outcome")
			}
		})
	}
}
func cTarget(c Config) string {
	return "https://fcm.googleapis.com/v1/projects/" + c.ProjectID + "/messages:send"
}
func TestTransportUncertaintyBoundsAndQuotaDeferral(t *testing.T) {
	config, request := transportFixture(t)
	c := makeClient(t, config, roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("secret underlying failure") }))
	out := send(t, c, request)
	if out.Disposition != Unconfirmed || out.PhysicalRequests != 1 || strings.Contains(fmt.Sprint(out), "secret") {
		t.Fatal(out)
	}
	oversized := makeClient(t, config, roundTripFunc(func(*http.Request) (*http.Response, error) { return response(503, strings.Repeat("x", 70*1024)), nil }))
	out = send(t, oversized, request)
	if out.Disposition != Unconfirmed || out.PhysicalRequests != 1 {
		t.Fatal(out)
	}
	quota := makeClient(t, config, roundTripFunc(func(*http.Request) (*http.Response, error) {
		r := response(429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`)
		r.Header.Set("Retry-After", "120")
		return r, nil
	}))
	out = send(t, quota, request)
	if out.RetryAt.Before(time.Now().Add(119 * time.Second)) {
		t.Fatal("provider floor shortened")
	}
	permit, err := quota.Acquire(context.Background())
	if permit != nil || !errors.Is(err, ErrDeferred) {
		t.Fatal("quota advice did not defer new claims")
	}
}
func TestPermitCopiesAndBoundedConcurrency(t *testing.T) {
	config, request := transportFixture(t)
	config.Concurrency = 4
	var active, maximum, calls atomic.Int32
	c := makeClient(t, config, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		n := active.Add(1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		defer active.Add(-1)
		calls.Add(1)
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(40 * time.Millisecond):
			return response(200, `{"name":"projects/fixture-project/messages/id"}`), nil
		}
	}))
	permit, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	copyPermit := *permit
	var wg sync.WaitGroup
	outputs := make(chan Outcome, 2)
	wg.Go(func() { outputs <- permit.Send(context.Background(), request) })
	wg.Go(func() { outputs <- copyPermit.Send(context.Background(), request) })
	wg.Wait()
	close(outputs)
	accepted := 0
	for o := range outputs {
		if o.Disposition == Accepted {
			accepted++
		}
	}
	if accepted != 1 || calls.Load() != 1 {
		t.Fatal("copied permit reused external I/O")
	}
	var permits []*Permit
	for range config.Concurrency {
		p, err := c.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		permits = append(permits, p)
	}
	if p, err := c.Acquire(context.Background()); p != nil || !errors.Is(err, ErrDeferred) {
		t.Fatal("unbounded admission")
	}
	for _, p := range permits {
		wg.Go(func() { p.Send(context.Background(), request) })
	}
	wg.Wait()
	if maximum.Load() > int32(config.Concurrency) || maximum.Load() < 2 {
		t.Fatal("unexpected HTTP concurrency", maximum.Load())
	}
	unused, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	copied := *unused
	unused.Release()
	copied.Release()
	if copied.Send(context.Background(), request).Reason != PermitUsed {
		t.Fatal("released permit reused")
	}
}
func TestCloseCancelsJoinsAndRevokesUnusedPermit(t *testing.T) {
	config, request := transportFixture(t)
	started := make(chan struct{})
	joined := make(chan struct{})
	var closed atomic.Int32
	source := tokenFunc(func(ctx context.Context) (*auth.Token, error) {
		close(started)
		<-ctx.Done()
		close(joined)
		return nil, ctx.Err()
	})
	c, err := newClient(config, source, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected network call")
		return nil, errTransport
	}), func() { closed.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	unused, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sending, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan Outcome, 1)
	go func() { done <- sending.Send(context.Background(), request) }()
	<-started
	c.Close()
	select {
	case <-joined:
	default:
		t.Fatal("credential callback abandoned")
	}
	out := <-done
	if out.Disposition != NotSubmitted || out.PhysicalRequests != 0 {
		t.Fatal(out)
	}
	if out := unused.Send(context.Background(), request); out.Reason != Closed {
		t.Fatal("unused permit not revoked")
	}
	c.Close()
	if closed.Load() != 1 {
		t.Fatal("HTTP cleanup repeated")
	}
	if p, err := c.Acquire(context.Background()); p != nil || !errors.Is(err, ErrClosed) {
		t.Fatal("admission after close")
	}
}
func TestRetryAdviceAndBreakerGenerations(t *testing.T) {
	now := time.Unix(2000000000, 0)
	deadline := now.Add(time.Hour)
	for _, tt := range []struct {
		header string
		want   time.Time
	}{
		{"0", now.Add(time.Minute)}, {"120", now.Add(120 * time.Second)}, {"invalid", now.Add(time.Minute)},
		{"999999999999999999999999999999999", deadline}, {now.Add(2 * time.Hour).UTC().Format(http.TimeFormat), deadline},
	} {
		if got := retryAt(tt.header, now, time.Minute, deadline); !got.Equal(tt.want) {
			t.Fatal("wrong retry time", got, tt.want)
		}
	}
	b := breaker{threshold: 2, cooldown: 30 * time.Second}
	old, _, _ := b.acquire(now)
	a, _, _ := b.acquire(now)
	z, _, _ := b.acquire(now)
	failure := Outcome{Disposition: Rejected, Reason: Unavailable}
	b.finish(a, failure, now)
	b.finish(z, failure, now)
	b.finish(old, Outcome{Disposition: Accepted}, now)
	if _, _, ok := b.acquire(now.Add(time.Second)); ok {
		t.Fatal("stale result closed newer breaker")
	}
	probe, _, ok := b.acquire(now.Add(31 * time.Second))
	if !ok {
		t.Fatal("missing half-open probe")
	}
	if _, retry, ok := b.acquire(now.Add(31 * time.Second)); ok || !retry.After(now.Add(31*time.Second)) {
		t.Fatal("a concurrent half-open probe must receive a future retry time")
	}
	b.finish(probe, Outcome{}, now.Add(31*time.Second))
	probe, _, ok = b.acquire(now.Add(31 * time.Second))
	if !ok {
		t.Fatal("neutral release retained probe")
	}
	b.finish(probe, Outcome{Disposition: Accepted}, now.Add(31*time.Second))
	if _, _, ok := b.acquire(now.Add(31 * time.Second)); !ok {
		t.Fatal("success did not close breaker")
	}
}

func TestQuotaHoldSurvivesDeliveryDeadline(t *testing.T) {
	config, request := transportFixture(t)
	request.Deadline = time.Now().Add(time.Second)
	c := makeClient(t, config, roundTripFunc(func(*http.Request) (*http.Response, error) {
		r := response(429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`)
		r.Header.Set("Retry-After", "120")
		return r, nil
	}))
	out := send(t, c, request)
	if !out.RetryAt.Equal(request.Deadline) {
		t.Fatal("delivery must terminalize at its deadline")
	}
	p, err := c.Acquire(context.Background())
	var deferred *Deferred
	if p != nil || !errors.As(err, &deferred) || deferred.RetryAt.Before(time.Now().Add(119*time.Second)) {
		t.Fatal("delivery expiry shortened project quota hold")
	}
}

func TestInvalidCredentialsNeverReachFCM(t *testing.T) {
	for _, name := range []string{"nil", "error", "type", "expired", "short lifetime", "whitespace", "oversized"} {
		t.Run(name, func(t *testing.T) {
			config, request := transportFixture(t)
			source := tokenFunc(func(context.Context) (*auth.Token, error) {
				token, _ := fixtureToken(context.Background())
				switch name {
				case "nil":
					return nil, nil
				case "error":
					return nil, errors.New("secret credential failure")
				case "type":
					token.Type = "Basic"
				case "expired":
					token.Expiry = time.Now().Add(-time.Second)
				case "short lifetime":
					token.Expiry = time.Now().Add(time.Second)
				case "whitespace":
					token.Value = "token\r\nsecret"
				case "oversized":
					token.Value = strings.Repeat("x", 16*1024+1)
				}
				return token, nil
			})
			c, err := newClient(config, source, roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("invalid credential reached HTTP")
				return nil, errTransport
			}), func() {})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			out := send(t, c, request)
			if out.Disposition != NotSubmitted || out.Reason != CredentialFailure || out.PhysicalRequests != 0 {
				t.Fatal(out)
			}
		})
	}
}
