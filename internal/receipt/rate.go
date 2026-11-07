package receipt

import "hello/internal/ratelimit"

// RateWindow bounds receipt requests per key and per process.
type RateWindow = ratelimit.Window

// NewRateWindow validates the per-replica receipt budget.
func NewRateWindow(limit int) (*RateWindow, error) {
	window, err := ratelimit.New(limit)
	if err != nil {
		return nil, ErrConfiguration
	}
	return window, nil
}
