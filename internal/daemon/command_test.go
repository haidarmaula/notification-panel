package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"hello/internal/deployment"
)

func TestCommandArgumentAndRoleBoundary(t *testing.T) {
	for _, tt := range []struct {
		name              string
		args              []string
		loaded            *Configuration
		loadErr           error
		want, loads, runs int
	}{
		{name: "help", args: []string{"--help"}, want: 0},
		{name: "override", args: []string{"--config=secret-value"}, want: 2},
		{name: "positional", args: []string{"secret-value"}, want: 2},
		{name: "policy failure", loadErr: errors.New("secret-value"), want: 1, loads: 1},
		{name: "nil policy", want: 1, loads: 1},
		{name: "wrong role", loaded: &Configuration{role: deployment.Scheduler}, want: 1, loads: 1},
		{name: "worker", loaded: &Configuration{role: deployment.Worker}, want: 0, loads: 1, runs: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, logs bytes.Buffer
			loads, runs := 0, 0
			code := command(context.Background(), deployment.Worker, tt.args, &out, &logs,
				func(role deployment.Component) (*Configuration, error) { loads++; return tt.loaded, tt.loadErr },
				func(*Configuration, context.Context, *slog.Logger) error { runs++; return nil })
			if code != tt.want || loads != tt.loads || runs != tt.runs {
				t.Fatalf("code=%d loads=%d runs=%d", code, loads, runs)
			}
			if strings.Contains(out.String()+logs.String(), "secret-value") {
				t.Fatal("unsafe error or argv exposure")
			}
		})
	}
}

func TestCommandCancellationDoesNotHideDrainFailure(t *testing.T) {
	for _, drain := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		var out, logs bytes.Buffer
		code := command(ctx, deployment.Worker, nil, &out, &logs,
			func(deployment.Component) (*Configuration, error) {
				return &Configuration{role: deployment.Worker}, nil
			},
			func(*Configuration, context.Context, *slog.Logger) error {
				cancel()
				if drain {
					return errors.Join(context.Canceled, errDrain)
				}
				return context.Canceled
			})
		cancel()
		want := 0
		if drain {
			want = 1
		}
		if code != want {
			t.Fatalf("drain=%v exit=%d", drain, code)
		}
	}
}

func TestRunRejectsCancellationAndChangedEnvironmentBeforeIO(t *testing.T) {
	clearPolicyEnvironment(t)
	cfg, err := Load(deployment.Worker, strings.NewReader(policyJSON(t, daemonPolicy(deployment.Worker))))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cfg.Run(ctx, logger); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	t.Setenv("PGHOST", "secret-value")
	if err := cfg.Run(context.Background(), logger); !errors.Is(err, ErrConfiguration) {
		t.Fatal(err)
	}
}
