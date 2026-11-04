package kafka

import (
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
