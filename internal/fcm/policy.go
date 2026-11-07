// Package fcm provides a route-bound, bounded single-device Firebase transport.
// It does not grant DB execution authority or enable a delivery worker.
package fcm

import (
	"context"
	"errors"
	"sync"
	"time"

	"hello/internal/pushpayload"
)

var (
	ErrConfiguration = errors.New("invalid FCM transport configuration")
	ErrClosed        = errors.New("FCM transport is closed")
	ErrDeferred      = errors.New("FCM submission deferred")
)

// Observer receives bounded local transport evidence. Implementations must return
// promptly. Neither callback receives provider identifiers, credentials or payloads.
type Observer interface {
	LogicalResult(Disposition, Reason, time.Duration)
	PhysicalStart()
	PhysicalFinish(int, time.Duration)
}

// Config is explicit per-project policy; replica/client quota allocation is external.
type Config struct {
	Observer          Observer
	ProjectID         string
	Environment       string
	Routes            []pushpayload.Route
	RequestsPerSecond int
	Concurrency       int
	RequestTimeout    time.Duration
	BreakerThreshold  int
	BreakerCooldown   time.Duration
}

// Validate checks the project's pacing, concurrency, timeout and breaker bounds
// together with the allowlisted routes, without allocating a client.
func (c Config) Validate() error {
	if !projectLabel(c.ProjectID) || c.Environment == "" || len(c.Routes) < 1 || len(c.Routes) > 64 ||
		c.RequestsPerSecond < 1 || c.RequestsPerSecond > 10000 || c.Concurrency < 1 || c.Concurrency > 128 ||
		c.RequestTimeout < 10*time.Second || c.RequestTimeout > 30*time.Second || c.BreakerThreshold < 1 || c.BreakerThreshold > 100 ||
		c.BreakerCooldown < 10*time.Second || c.BreakerCooldown > time.Hour {
		return ErrConfiguration
	}
	seen := make(map[pushpayload.Route]bool, len(c.Routes))
	for _, r := range c.Routes {
		if _, err := pushpayload.New(r); err != nil || r.Environment != c.Environment || seen[r] {
			return ErrConfiguration
		}
		seen[r] = true
	}
	return nil
}
func projectLabel(value string) bool {
	if len(value) < 1 || len(value) > 63 {
		return false
	}
	for _, b := range []byte(value) {
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-') {
			return false
		}
	}
	return value[0] != '-' && value[len(value)-1] != '-'
}

// Deferred is a pre-claim scheduling hint, not a charged provider failure.
type Deferred struct{ RetryAt time.Time }

func (d *Deferred) Error() string { return ErrDeferred.Error() }
func (d *Deferred) Unwrap() error { return ErrDeferred }

type pace struct {
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

func (p *pace) wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.mu.Lock()
		now := time.Now()
		delay := p.next.Sub(now)
		if delay <= 0 {
			p.next = now.Add(p.interval)
			p.mu.Unlock()
			return nil
		}
		p.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type breakerToken struct {
	generation uint64
	probe      bool
}
type breaker struct {
	mu                  sync.Mutex
	generation          uint64
	failures, threshold int
	until               time.Time
	probe               bool
	cooldown            time.Duration
}

func (b *breaker) acquire(now time.Time) (breakerToken, time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.until.IsZero() {
		if now.Before(b.until) {
			return breakerToken{}, b.until, false
		}
		if b.probe {
			// A half-open probe is still running. The original cooldown has
			// elapsed, so persist a future recheck for other due deliveries.
			return breakerToken{}, now.Add(time.Second), false
		}
		b.probe = true
		return breakerToken{generation: b.generation, probe: true}, time.Time{}, true
	}
	return breakerToken{generation: b.generation}, time.Time{}, true
}
func (b *breaker) finish(token breakerToken, outcome Outcome, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if token.generation != b.generation {
		return
	}
	if token.probe {
		b.probe = false
	}
	if outcome.Disposition == Accepted {
		b.failures = 0
		b.until = time.Time{}
		if token.probe {
			b.generation++
		}
		return
	}
	if !outcome.retryable() {
		return
	}
	b.failures++
	if token.probe || b.failures >= b.threshold {
		b.until = now.Add(b.cooldown)
		b.probe = false
		b.generation++
	}
}
