package receipt

import (
	"sync"
	"time"
)

// RateWindow bounds requests per key per process. A full window rejects new
// identities until the next minute, which bounds memory under key churn.
type RateWindow struct {
	mu     sync.Mutex
	limit  int
	minute int64
	used   map[string]int
}

// NewRateWindow requires an explicit positive limit for each deployed replica.
func NewRateWindow(limit int) (*RateWindow, error) {
	if limit < 1 || limit > 60000 {
		return nil, ErrConfiguration
	}
	return &RateWindow{limit: limit, used: make(map[string]int)}, nil
}

// Allow counts the key against the current minute and reports seconds until a retry.
func (r *RateWindow) Allow(key string, now time.Time) (bool, int) {
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
