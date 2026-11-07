//go:build linux || darwin

package runloop

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A descriptor inherited across exec is normally blocking. os.Pipe alone
// creates a pollable Go file and would miss that production behavior.
func TestInheritedFullOutputPipe(t *testing.T) {
	if os.Getenv("HELLO_TEST_INHERITED_PIPE") == "1" {
		restore, err := PrepareCommandOutputs()
		if err != nil {
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		join := InterruptOutputOnCancel(ctx, os.Stdout, os.Stderr)
		_, err = os.Stdout.Write(make([]byte, 4096))
		join()
		cancel()
		restore()
		if err == nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	fd := int(writer.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	for {
		_, err = unix.Write(fd, make([]byte, 4096))
		if errors.Is(err, unix.EAGAIN) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInheritedFullOutputPipe$")
	cmd.Env = append(os.Environ(), "HELLO_TEST_INHERITED_PIPE=1")
	cmd.Stdout = writer
	if err := cmd.Run(); err != nil {
		t.Fatalf("inherited pipe blocked process exit: %v (%v)", err, ctx.Err())
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_NONBLOCK != 0 {
		t.Fatalf("inherited status flags were not restored: %v", err)
	}
}
