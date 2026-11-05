package runloop

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"
)

func TestSuperviseBacksOffWithoutLoggingPrivateErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var log bytes.Buffer
	calls := 0
	var times []time.Time
	run := func(context.Context) error {
		calls++
		times = append(times, time.Now())
		if calls == 3 {
			cancel()
			return nil
		}
		return errors.New("postgres://user:secret@host payload")
	}
	err := Supervise(ctx, "admission", run, RetryPolicy{MinimumDelay: 10 * time.Millisecond, MaximumDelay: 40 * time.Millisecond}, slog.New(slog.NewJSONHandler(&log, nil)))
	if !errors.Is(err, context.Canceled) || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if times[1].Sub(times[0]) < 10*time.Millisecond || times[2].Sub(times[1]) < 10*time.Millisecond {
		t.Fatal("dependency retry busy-looped")
	}
	if strings.Contains(log.String(), "secret") || !strings.Contains(log.String(), "admission") {
		t.Fatal("unsafe or missing retry signal")
	}
}

func TestRunTaskBoundsCallbacksAndRetainsNormalPolling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var log bytes.Buffer
	calls := 0
	task := Task{Name: "promotion", Interval: 10 * time.Millisecond, Timeout: 50 * time.Millisecond, Run: func(ctx context.Context) error {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 50*time.Millisecond {
			t.Error("callback lost its bound")
		}
		if calls == 1 {
			return errors.New("secret query")
		}
		if calls == 3 {
			cancel()
		}
		return nil
	}}
	err := RunTask(ctx, task, RetryPolicy{MinimumDelay: 10 * time.Millisecond, MaximumDelay: 40 * time.Millisecond}, slog.New(slog.NewJSONHandler(&log, nil)))
	if !errors.Is(err, context.Canceled) || calls != 3 || strings.Contains(log.String(), "secret") {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestRunGroupJoinsSiblingCleanup(t *testing.T) {
	entered := make(chan struct{})
	cleaning := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	failed := errors.New("worker stopped")
	go func() {
		done <- RunGroup(context.Background(), func(context.Context) error { <-entered; return failed }, func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			close(cleaning)
			<-release
			return ctx.Err()
		})
	}()
	select {
	case <-cleaning:
	case <-time.After(time.Second):
		t.Fatal("sibling did not cancel")
	}
	select {
	case <-done:
		t.Fatal("group returned before cleanup joined")
	default:
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, failed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("group did not return")
	}
}
func TestRunLoopsRejectInvalidDependenciesBeforeStarting(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&log, nil))
	policy := RetryPolicy{MinimumDelay: time.Millisecond, MaximumDelay: time.Second}
	if err := Supervise(context.Background(), "forged\nname", func(context.Context) error { t.Fatal("started invalid component"); return nil }, policy, logger); err == nil {
		t.Fatal("accepted invalid name")
	}
	if err := RunTask(context.Background(), Task{Name: "tick", Interval: time.Second, Timeout: time.Second}, policy, logger); err == nil {
		t.Fatal("accepted missing task")
	}
	if err := RunGroup(context.Background(), func(context.Context) error { t.Fatal("partially started invalid group"); return nil }, nil); err == nil {
		t.Fatal("accepted nil sibling")
	}
}
func TestRetryDelayRemainsBounded(t *testing.T) {
	policy := RetryPolicy{MinimumDelay: time.Second, MaximumDelay: time.Minute}
	for _, failures := range []int{0, 1, 10, math.MaxInt} {
		for range 20 {
			delay := retryDelay(policy, failures)
			if delay < policy.MinimumDelay || delay > policy.MaximumDelay {
				t.Fatalf("unbounded retry: %v", delay)
			}
		}
	}
}
