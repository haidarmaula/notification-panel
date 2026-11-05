package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExecutionPolicyRejectsUnsafeBounds(t *testing.T) {
	valid := ExecutionPolicy{LeaseDuration: time.Minute, RetryFloor: 10 * time.Second, MaxAttempts: 5}
	for _, change := range []func(*ExecutionPolicy){
		func(p *ExecutionPolicy) { p.LeaseDuration = 29 * time.Second },
		func(p *ExecutionPolicy) { p.LeaseDuration = 6 * time.Minute },
		func(p *ExecutionPolicy) { p.LeaseDuration += time.Nanosecond },
		func(p *ExecutionPolicy) { p.RetryFloor = 9 * time.Second },
		func(p *ExecutionPolicy) { p.RetryFloor = 25 * time.Hour },
		func(p *ExecutionPolicy) { p.MaxAttempts = 0 },
		func(p *ExecutionPolicy) { p.MaxAttempts = 101 },
	} {
		p := valid
		change(&p)
		if p.Validate() == nil {
			t.Fatal("accepted unsafe execution policy")
		}
	}
	if valid.Validate() != nil {
		t.Fatal("rejected explicit bounded policy")
	}
	if service, err := NewExecution(nil, valid); service != nil || err == nil {
		t.Fatal("accepted missing database")
	}
}

func TestExecutionZeroValueRejectsCalls(t *testing.T) {
	id, err := newReplayClaimToken()
	if err != nil {
		t.Fatal(err)
	}
	service := new(Execution)
	lease, err := service.Acquire(context.Background(), id, 1)
	if lease != nil || !errors.Is(err, ErrExecutionInput) {
		t.Fatal("zero execution accepted acquisition")
	}
	if err := service.Renew(context.Background(), new(ExecutionLease)); !errors.Is(err, ErrExecutionInput) {
		t.Fatal(err)
	}
	reservation, err := service.Reserve(context.Background(), new(ExecutionLease), id)
	if reservation != nil || !errors.Is(err, ErrExecutionInput) {
		t.Fatal("zero execution accepted reservation")
	}
}
