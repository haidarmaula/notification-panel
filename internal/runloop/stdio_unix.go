//go:build linux || darwin

package runloop

import (
	"errors"
	"log"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// PrepareCommandOutputs makes inherited blocking pipes and sockets interruptible
// by Go's file poller. Call once in main, before starting application goroutines.
// The returned cleanup restores inherited descriptor status flags at process exit.
func PrepareCommandOutputs() (func(), error) {
	stdout, closeOut, err := pollableStream(os.Stdout)
	if err != nil {
		return nil, err
	}
	stderr, closeErr, err := pollableStream(os.Stderr)
	if err != nil {
		closeOut()
		return nil, err
	}
	os.Stdout, os.Stderr = stdout, stderr
	log.SetOutput(stderr)
	return func() { closeErr(); closeOut() }, nil
}

// PrepareCommandInput also adopts a pollable input stream for commands that
// read their request from stdin. Its owner can then interrupt a partial read.
func PrepareCommandInput() (func(), error) {
	in, cleanup, err := pollableStream(os.Stdin)
	if err != nil {
		return nil, err
	}
	os.Stdin = in
	return cleanup, nil
}

func pollableStream(f *os.File) (*os.File, func(), error) {
	noop := func() {}
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 || f.SetDeadline(time.Time{}) == nil {
		return f, noop, nil
	}
	// NewFile only registers an inherited descriptor with the poller if it is
	// already nonblocking. Keep the original os.File alive and use a duplicate
	// with independent Go ownership for cancellation and close.
	raw, err := f.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	var dup, flags int
	var setupErr error
	err = raw.Control(func(fd uintptr) {
		flags, setupErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
		if setupErr != nil {
			return
		}
		dup, setupErr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 3)
		if setupErr != nil {
			return
		}
		if setupErr = unix.SetNonblock(dup, true); setupErr != nil {
			_ = unix.Close(dup)
		}
	})
	if err != nil || setupErr != nil {
		return nil, nil, errors.Join(err, setupErr)
	}
	out := os.NewFile(uintptr(dup), f.Name())
	cleanup := func() {
		_ = out.Close()
		if flags&unix.O_NONBLOCK == 0 {
			_ = raw.Control(func(fd uintptr) { _ = unix.SetNonblock(int(fd), false) })
		}
	}
	if err = out.SetDeadline(time.Time{}); err != nil {
		cleanup()
		return nil, nil, err
	}
	return out, cleanup, nil
}
