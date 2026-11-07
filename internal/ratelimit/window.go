// Package ratelimit provides a bounded per-process limit for untrusted request sources.
package ratelimit

import (
	"errors"
	"sync"
	"time"
)

// Window bounds requests per key per process. A full window rejects new
// identities until the next minute, which bounds memory under key churn.
type Window struct {
	mu     sync.Mutex
	limit  int
	minute int64
	used   map[string]int
}

// New requires an explicit positive limit for each deployed replica.
func New(limit int) (*Window, error) {
	if limit < 1 || limit > 60000 {
		return nil, errors.New("invalid request rate limit")
	}
	return &Window{limit: limit, used: make(map[string]int)}, nil
}

// Allow counts the key against the current minute and reports seconds until a retry.
func (r *Window) Allow(key string, now time.Time) (bool, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	second := now.Unix()
	minute := second / 60
	if minute > r.minute {
		clear(r.used)
		r.minute = minute
	}
	n, exists := r.used[key]
	if n >= r.limit || (!exists && len(r.used) >= 65536) {
		return false, 60 - int(second%60)
	}
	r.used[key] = n + 1
	return true, 0
}
