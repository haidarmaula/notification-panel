package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"hello/internal/dispatch"
	"hello/internal/requeue"
)

func TestHelpNeedsNoDeploymentConfiguration(t *testing.T) {
	input, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.CreateTemp(t.TempDir(), "report")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	var diag bytes.Buffer
	if code := run(context.Background(), []string{"--help"}, input, output, &diag, func(context.Context, dispatch.ReplayRequest, string, bool) (requeue.Session, error) {
		t.Fatal("help loaded deployment credentials")
		return nil, nil
	}); code != 0 || diag.Len() != 0 {
		t.Fatalf("code=%d diag=%q", code, diag.String())
	}
	if _, err := output.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var help bytes.Buffer
	help.ReadFrom(output)
	if !strings.Contains(help.String(), "stdin") {
		t.Fatal("missing command help")
	}
}
func TestCancellationInterruptsIncompleteInput(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	output, err := os.CreateTemp(t.TempDir(), "report")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var diag bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, nil, input, output, &diag, func(context.Context, dispatch.ReplayRequest, string, bool) (requeue.Session, error) {
			t.Error("incomplete input opened deployment session")
			return nil, nil
		})
	}()
	if _, err := writer.WriteString("{"); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case code := <-done:
		if code != 1 || !strings.Contains(diag.String(), "canceled") {
			t.Fatalf("code=%d diag=%q", code, diag.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("command failed to join canceled input")
	}
}
