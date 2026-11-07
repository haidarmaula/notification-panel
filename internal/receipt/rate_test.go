package receipt

import (
	"testing"
	"time"
)

func TestRateWindow(t *testing.T) {
	w, err := NewRateWindow(2)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(100, 0)
	for _, key := range []string{"a", "a", "b"} {
		if ok, _ := w.Allow(key, at); !ok {
			t.Fatalf("unexpected limit for %s", key)
		}
	}
	if ok, delay := w.Allow("a", at); ok || delay != 20 {
		t.Fatalf("over limit: %v, %d", ok, delay)
	}
	if ok, _ := w.Allow("a", at.Add(20*time.Second)); !ok {
		t.Fatal("window failed to reset")
	}
}
