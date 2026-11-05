//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"hello/internal/deployment"
)

func TestProcessCancellationInterruptsFullOutputPipes(t *testing.T) {
	for _, help := range []bool{false, true} {
		t.Run(map[bool]string{false: "diagnostics", true: "help"}[help], func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			fd := int(writer.Fd())
			if err := syscall.SetNonblock(fd, true); err != nil {
				t.Fatal(err)
			}
			block := make([]byte, 4096)
			for {
				_, err := syscall.Write(fd, block)
				if errors.Is(err, syscall.EAGAIN) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			other, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			out, diag := other, writer
			args := []string{"invalid"}
			if help {
				out, diag = writer, other
				args = []string{"--help"}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan int, 1)
			go func() { done <- Process(ctx, deployment.Worker, args, out, diag) }()
			select {
			case <-done:
				t.Fatal("full pipe unexpectedly returned")
			case <-time.After(30 * time.Millisecond):
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("blocked stream prevented process shutdown")
			}
		})
	}
}
