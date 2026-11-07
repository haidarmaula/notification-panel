// Package runloop owns bounded background polling, supervised restarts and joined
// process shutdown. Callbacks own their resources and must honor cancellation.
package runloop

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
)

var errConfiguration = errors.New("invalid background loop configuration")
var errStopped = errors.New("background component stopped unexpectedly")

// RetryPolicy bounds exponential delays with jitter, including at the cap.
// The minimum delay is 10ms; the maximum may not exceed one hour.
type RetryPolicy struct{ MinimumDelay, MaximumDelay time.Duration }

// Validate rejects busy retry loops and durations that cannot be safely capped.
func (p RetryPolicy) Validate() error {
	if p.MinimumDelay < 10*time.Millisecond || p.MaximumDelay < p.MinimumDelay || p.MaximumDelay > time.Hour {
		return errConfiguration
	}
	return nil
}

// Observer receives transitions for supervised and polling loops.
type Observer interface {
	Phase(name, outcome string, next time.Time)
}

// Task is a single non-overlapping polling callback. Timeout bounds its context;
// Run must honor cancellation and finish its cleanup before returning. A timeout
// wrapper cannot safely abandon an uncooperative callback in another goroutine.
type Task struct {
	Observer Observer
	Name     string
	Interval time.Duration
	Timeout  time.Duration
	Run      func(context.Context) error
	// RunPage is an alternative to Run for callers that drain bounded pages.
	// Return true only after acknowledged progress with more work indicated.
	RunPage        func(context.Context) (bool, error)
	ActiveInterval time.Duration
}

// Supervise restarts an entire stopped component after it has joined its owned
// resources. It never retries an individual transaction or reconstructs an I/O
// execution proof. Errors are signaled without logging private dependency text.
func Supervise(ctx context.Context, name string, run func(context.Context) error, policy RetryPolicy, logger *slog.Logger) error {
	return SuperviseObserved(ctx, name, run, policy, logger, nil)
}

// SuperviseObserved records generation start, failure and bounded restart waits.
// A generation may legitimately wait without deliveries for an unlimited time.
func SuperviseObserved(ctx context.Context, name string, run func(context.Context) error, policy RetryPolicy, logger *slog.Logger, observer Observer) error {
	if !validName(name) || run == nil || policy.Validate() != nil || logger == nil {
		return errConfiguration
	}
	if observer != nil {
		defer observer.Phase(name, "stopped", time.Time{})
	}
	for failures := 0; ; failures = min(failures+1, 20) {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		started := time.Now()
		if observer != nil {
			observer.Phase(name, "running", time.Time{})
		}
		err := run(ctx)
		if time.Since(started) >= policy.MaximumDelay {
			failures = 0
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		delay := retryDelay(policy, failures)
		if observer != nil {
			observer.Phase(name, "error", time.Now().Add(delay))
		}
		reason := "returned_error"
		if err == nil {
			reason = "unexpected_return"
		}
		logger.ErrorContext(ctx, "background component restarting", "component", name, "reason", reason, "attempt", failures+1, "retry_after", delay)
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
}

// RunTask polls immediately, then waits between completed calls. Failure backs
// off; success resets the failure streak. Callbacks must re-read durable state
// on each poll, rather than blindly repeat a failed external-I/O transaction.
func RunTask(ctx context.Context, task Task, policy RetryPolicy, logger *slog.Logger) error {
	if !validName(task.Name) || task.Interval < 10*time.Millisecond || task.Timeout <= 0 || task.Timeout > time.Hour || policy.Validate() != nil || logger == nil {
		return errConfiguration
	}
	run := task.RunPage
	if run == nil {
		if task.Run == nil || task.ActiveInterval != 0 {
			return errConfiguration
		}
		run = func(ctx context.Context) (bool, error) { return false, task.Run(ctx) }
	} else if task.Run != nil || task.ActiveInterval < 10*time.Millisecond || task.ActiveInterval > task.Interval {
		return errConfiguration
	}
	if task.Observer != nil {
		defer task.Observer.Phase(task.Name, "stopped", time.Time{})
	}
	failures := 0
	for {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if task.Observer != nil {
			task.Observer.Phase(task.Name, "running", time.Now().Add(task.Timeout))
		}
		callCtx, cancel := context.WithTimeout(ctx, task.Timeout)
		more, err := run(callCtx)
		if err == nil {
			err = callCtx.Err()
		}
		cancel()
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		delay := task.Interval
		if err != nil {
			delay = retryDelay(policy, failures)
			failures = min(failures+1, 20)
			// Underlying errors can contain connection strings, tokens or payloads.
			logger.ErrorContext(ctx, "background task failed", "component", task.Name, "attempt", failures, "retry_after", delay)
		} else {
			failures = 0
			if more {
				delay = task.ActiveInterval
			}
		}
		if task.Observer != nil {
			outcome := "success"
			if err != nil {
				outcome = "error"
			}
			task.Observer.Phase(task.Name, outcome, time.Now().Add(delay))
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
}

// RunGroup starts all validated components, cancels siblings when one stops and
// joins every callback before returning. Resource owners can then safely close
// their shared writer/pool. It does not detach cleanup after a timeout.
func RunGroup(parent context.Context, runs ...func(context.Context) error) error {
	if len(runs) == 0 {
		return errConfiguration
	}
	for _, run := range runs {
		if run == nil {
			return errConfiguration
		}
	}
	if parent.Err() != nil {
		return context.Cause(parent)
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	var joined sync.WaitGroup
	for _, run := range runs {
		joined.Go(func() {
			err := run(ctx)
			if err == nil && ctx.Err() == nil {
				err = errStopped
			}
			if err != nil {
				cancel(err)
			}
		})
	}
	joined.Wait()
	return context.Cause(ctx)
}

func retryDelay(policy RetryPolicy, failures int) time.Duration {
	base := policy.MinimumDelay
	for range min(max(failures, 0), 20) {
		base = min(base*2, policy.MaximumDelay)
	}
	floor := max(policy.MinimumDelay, base/2)
	return floor + time.Duration(rand.Int64N(int64(base-floor)+1))
}
func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}
func validName(name string) bool {
	if len(name) < 1 || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
