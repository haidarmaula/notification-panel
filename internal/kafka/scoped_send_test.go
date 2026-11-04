package kafka

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScopedSendValidation(t *testing.T) {
	base := ScopedSendRequested{SchemaVersion: 1, ApplicationID: "11111111-1111-4111-8111-111111111111", NotificationID: 42,
		OriginEpoch: "22222222-2222-4222-8222-222222222222", SourceEpoch: "33333333-3333-4333-8333-333333333333",
		RequestedAt: time.Now().UTC(), DeadlineAt: time.Now().UTC().Add(time.Hour)}
	require.NoError(t, base.Validate())
	for _, change := range []func(*ScopedSendRequested){
		func(e *ScopedSendRequested) { e.SchemaVersion = 0 },
		func(e *ScopedSendRequested) { e.NotificationID = 0 },
		func(e *ScopedSendRequested) { e.ApplicationID = "" },
		func(e *ScopedSendRequested) { e.OriginEpoch = "00000000-0000-0000-0000-000000000000" },
		func(e *ScopedSendRequested) { e.SourceEpoch = "bad" },
		func(e *ScopedSendRequested) { e.RequestedAt = time.Time{} },
		func(e *ScopedSendRequested) { e.DeadlineAt = time.Time{} },
		func(e *ScopedSendRequested) { at := time.Time{}; e.ScheduledFor = &at },
	} {
		e := base
		change(&e)
		require.ErrorIs(t, e.Validate(), ErrScopedSendEnvelope)
	}
	// An expired scheduled acceptance still needs admission/expiry evidence.
	base.DeadlineAt = base.RequestedAt.Add(-time.Second)
	require.NoError(t, base.Validate())
}

func TestDecodeScopedSend(t *testing.T) {
	event := ScopedSendRequested{SchemaVersion: 1, ApplicationID: "11111111-1111-4111-8111-111111111111", NotificationID: 42,
		OriginEpoch: "22222222-2222-4222-8222-222222222222", SourceEpoch: "33333333-3333-4333-8333-333333333333",
		RequestedAt: time.Now().UTC(), DeadlineAt: time.Now().UTC().Add(time.Hour)}
	value, err := json.Marshal(event)
	require.NoError(t, err)
	decoded, err := DecodeScopedSend([]byte("notification_42"), append(value, '\n'))
	require.NoError(t, err)
	require.Equal(t, event, decoded)
	for name, payload := range map[string]string{
		"legacy":    `{"notification_id":42,"requested_at":"2030-01-01T00:00:00Z"}`,
		"null":      `null`,
		"array":     `[]`,
		"trailing":  string(value) + `{}`,
		"duplicate": strings.TrimSuffix(string(value), "}") + `,"notification_id":42}`,
		"unknown":   strings.TrimSuffix(string(value), "}") + `,"override":true}`,
		"too_large": strings.Repeat(" ", 65537),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeScopedSend([]byte("notification_42"), []byte(payload))
			require.ErrorIs(t, err, ErrScopedSendEnvelope)
		})
	}
	for _, key := range []string{"notification_0", "notification_042", "notification_43", "notification_42 ", "", "\xff"} {
		_, err := DecodeScopedSend([]byte(key), value)
		require.ErrorIs(t, err, ErrScopedSendEnvelope)
	}
}
