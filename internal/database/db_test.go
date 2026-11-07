package database

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// The server accepts a TCP connection but never completes the PostgreSQL
// handshake. Cancelling startup must also close the partial pool's connection.
func TestNewClosesStalledConnectionOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.(*net.TCPListener).SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		pool, err := New(ctx, "postgres://fixture:fixture@"+l.Addr().String()+"/fixture?sslmode=disable")
		if pool != nil {
			pool.Close()
			t.Error("unexpected usable pool")
		}
		result <- err
	}()
	conn, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled connection succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pool did not return after startup cancellation")
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, conn); err != nil {
		t.Fatalf("partial connection stayed open: %v", err)
	}
}
