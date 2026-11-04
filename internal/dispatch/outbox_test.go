package dispatch

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublishBackoffBounds(t *testing.T) {
	for _, attempts := range []int32{-1, 0, 1, 7, 8, 9, math.MaxInt32} {
		for range 20 {
			delay := publishBackoff(attempts)
			require.GreaterOrEqual(t, delay, time.Second)
			require.LessOrEqual(t, delay, 5*time.Minute)
			if attempts <= 0 {
				require.LessOrEqual(t, delay, 1250*time.Millisecond)
			}
		}
	}
	_, err := NewOutbox(nil, nil)
	require.Error(t, err)
}
