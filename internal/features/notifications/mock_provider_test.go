package notifications

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMockProvider_Send_ShortTokens_NoPanic is the regression test for C11.
// Every input in the table previously caused a slice-out-of-range panic
// because the logging line sliced token.PushToken[:8] unconditionally.
func TestMockProvider_Send_ShortTokens_NoPanic(t *testing.T) {
	p := NewMockProvider()

	cases := []struct {
		name   string
		tokens []DeviceTokenInfo
	}{
		{
			name:   "empty token",
			tokens: []DeviceTokenInfo{{UserID: 1, PushToken: ""}},
		},
		{
			name:   "single byte",
			tokens: []DeviceTokenInfo{{UserID: 1, PushToken: "x"}},
		},
		{
			name:   "seven bytes",
			tokens: []DeviceTokenInfo{{UserID: 1, PushToken: "1234567"}},
		},
		{
			name:   "exactly eight bytes",
			tokens: []DeviceTokenInfo{{UserID: 1, PushToken: "12345678"}},
		},
		{
			name:   "longer token",
			tokens: []DeviceTokenInfo{{UserID: 1, PushToken: "abcdefghijklmnop"}},
		},
		{
			name: "mixed lengths in one batch",
			tokens: []DeviceTokenInfo{
				{UserID: 1, PushToken: ""},
				{UserID: 2, PushToken: "a"},
				{UserID: 3, PushToken: "12345678"},
				{UserID: 4, PushToken: "abcdefghijklmnop"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, err := p.Send(context.Background(), SendRequest{
				NotificationID: 99,
				Tokens:         tc.tokens,
			})
			require.NoError(t, err)
			require.Len(t, results, len(tc.tokens))
			for i, r := range results {
				assert.Equal(t, "SENT", r.Status)
				assert.Equal(t, tc.tokens[i].UserID, r.UserID)
				assert.Equal(t, tc.tokens[i].PushToken, r.PushToken)
			}
		})
	}
}
