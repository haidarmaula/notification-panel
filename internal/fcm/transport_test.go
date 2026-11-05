package fcm

import (
	"cloud.google.com/go/auth"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRetimeBoundsAndPreservesPayload(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"message":null}`, `{"message":{"android":null}}`, `{"message":{"android":3}}`, strings.Repeat("x", 33*1024)} {
		t.Run(fmt.Sprintf("invalid-%d", len(raw))+raw[:min(len(raw), 25)], func(t *testing.T) {
			if _, err := retime(io.NopCloser(strings.NewReader(raw)), time.Now().Add(time.Hour)); err == nil {
				t.Fatal("invalid SDK body accepted")
			}
		})
	}
	raw := `{"message":{"token":"synthetic","android":{"ttl":"3600s","priority":"high"},"data":{"key":"value"}}}`
	body, err := retime(io.NopCloser(strings.NewReader(raw)), time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Message struct {
			Token   string
			Android struct{ TTL, Priority string }
			Data    map[string]string
		}
	}
	if json.Unmarshal(body, &got) != nil || got.Message.Android.TTL != "4s" || got.Message.Android.Priority != "high" || got.Message.Data["key"] != "value" || got.Message.Token != "synthetic" {
		t.Fatal("payload changed or expiry extended")
	}
	if _, err := retime(io.NopCloser(strings.NewReader(raw)), time.Now().Add(-time.Second)); err == nil {
		t.Fatal("expired request accepted")
	}
}

func TestCloseCancelsActiveHTTPAndPacing(t *testing.T) {
	config, request := transportFixture(t)
	started, joined := make(chan struct{}), make(chan struct{})
	c := makeClient(t, config, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(joined)
		return nil, r.Context().Err()
	}))
	permit, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan Outcome, 1)
	go func() { done <- permit.Send(context.Background(), request) }()
	<-started
	c.Close()
	select {
	case <-joined:
	default:
		t.Fatal("HTTP callback abandoned")
	}
	if out := <-done; out.Disposition != Unconfirmed || out.PhysicalRequests != 1 {
		t.Fatal(out)
	}

	p := pace{interval: time.Second}
	if err := p.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.wait(ctx); err != context.Canceled {
		t.Fatal("pacing ignored cancellation")
	}
}

func TestConfigurationAndCredentialBoundaries(t *testing.T) {
	for _, field := range []string{"project", "environment", "routes", "duplicate", "rate", "concurrency", "timeout", "threshold", "cooldown"} {
		t.Run(field, func(t *testing.T) {
			config, _ := transportFixture(t)
			switch field {
			case "project":
				config.ProjectID = "fixture/other"
			case "environment":
				config.Environment = "foreign"
			case "routes":
				config.Routes = nil
			case "duplicate":
				config.Routes = append(config.Routes, config.Routes[0])
			case "rate":
				config.RequestsPerSecond = 0
			case "concurrency":
				config.Concurrency = 0
			case "timeout":
				config.RequestTimeout = 0
			case "threshold":
				config.BreakerThreshold = 0
			case "cooldown":
				config.BreakerCooldown = 0
			}
			if _, err := newClient(config, tokenFunc(fixtureToken), roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("constructor sent request"); return nil, nil }), func() {}); err != ErrConfiguration {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
	t.Run("root override", func(t *testing.T) {
		t.Setenv("SSL_CERT_FILE", "/fixture/untrusted-roots")
		config, _ := transportFixture(t)
		if _, err := New(config, tokenFunc(fixtureToken)); err != ErrConfiguration {
			t.Fatal("inherited roots accepted")
		}
	})
	config, request := transportFixture(t)
	c := makeClient(t, config, roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("foreign route sent request"); return nil, nil }))
	request.Destination.RouteKey = "foreign"
	if out := send(t, c, request); out.Disposition != NotSubmitted || out.PhysicalRequests != 0 || out.Reason != InvalidPayload {
		t.Fatal(out)
	}
	copied := *c
	for _, v := range []any{c, copied} {
		encoded, err := json.Marshal(v)
		if err != nil || string(encoded) != `"[REDACTED]"` || fmt.Sprintf("%+v", v) != "[REDACTED]" || fmt.Sprintf("%#v", v) != "[REDACTED]" || v.(slog.LogValuer).LogValue().String() != "[REDACTED]" {
			t.Fatal("client formatting exposes state")
		}
	}
}

func TestPhysicalPacingAfterClaimDelay(t *testing.T) {
	config, request := transportFixture(t)
	config.RequestsPerSecond = 20
	var starts []time.Time
	c := makeClient(t, config, roundTripFunc(func(*http.Request) (*http.Response, error) {
		starts = append(starts, time.Now())
		return response(200, `{"name":"projects/fixture-project/messages/id"}`), nil
	}))
	first, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Both logical permits predate the first physical start. The independent
	// physical gate still prevents an immediate burst after claim work completes.
	if out := first.Send(context.Background(), request); out.Disposition != Accepted {
		t.Fatal(out)
	}
	if out := second.Send(context.Background(), request); out.Disposition != Accepted {
		t.Fatal(out)
	}
	if len(starts) != 2 || starts[1].Sub(starts[0]) < 45*time.Millisecond {
		t.Fatal("post-claim burst bypassed rate")
	}
}

func TestFinalAuthorizationRunsAfterCredentialsAndBoundsHTTP(t *testing.T) {
	config, request := transportFixture(t)
	credentialReady := false
	source := tokenFunc(func(context.Context) (*auth.Token, error) {
		credentialReady = true
		return fixtureToken(context.Background())
	})
	c, err := newClient(config, source, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	out := c.SendAuthorized(context.Background(), p, request, func(context.Context) (time.Time, error) {
		if !credentialReady {
			t.Error("authorization ran before refresh")
		}
		return time.Now().Add(30 * time.Millisecond), nil
	})
	if out.Disposition != Unconfirmed || out.PhysicalRequests != 1 || time.Since(started) > time.Second {
		t.Fatal(out)
	}
	p, err = c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out = c.SendAuthorized(context.Background(), p, request, func(context.Context) (time.Time, error) { return time.Time{}, errors.New("private DB fault") })
	if out.Disposition != NotSubmitted || out.Reason != AuthorizationFailure || out.PhysicalRequests != 0 {
		t.Fatal(out)
	}
}

func TestAuthorizedSendRejectsForeignPermit(t *testing.T) {
	config, request := transportFixture(t)
	blocked := roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected provider call")
		return nil, errTransport
	})
	first, second := makeClient(t, config, blocked), makeClient(t, config, blocked)
	if !first.Allows(config.Routes[0]) {
		t.Fatal("declared route missing")
	}
	route := config.Routes[0]
	route.Environment = "foreign"
	if first.Allows(route) {
		t.Fatal("foreign environment allowed")
	}
	permit, err := second.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer permit.Release()
	out := first.SendAuthorized(context.Background(), permit, request, func(context.Context) (time.Time, error) {
		t.Error("foreign permit authorized")
		return time.Now().Add(time.Minute), nil
	})
	if out.Disposition != NotSubmitted || out.PhysicalRequests != 0 || out.Reason != AuthorizationFailure {
		t.Fatal(out)
	}
	first.Close()
	if first.Allows(config.Routes[0]) {
		t.Fatal("closed client allows route")
	}
}
