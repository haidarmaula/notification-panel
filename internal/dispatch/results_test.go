package dispatch

import (
	"testing"
	"time"

	"hello/internal/database/sqlc"
	"hello/internal/fcm"
)

func TestResultStateAndRetryPolicy(t *testing.T) {
	now := time.Unix(2000000000, 0).UTC()
	for _, tc := range []struct {
		name, status string
		out          fcm.Outcome
		count        int32
		deadline     time.Duration
		want         string
		changed      bool
		reason       string
		floor        time.Duration
	}{
		{"accepted", "PENDING", fcm.Outcome{Disposition: fcm.Accepted, Reason: fcm.Success, ProviderMessageID: "projects/fixture/messages/id"}, 1, time.Hour, "SENT", true, "", 0},
		{"early opened acceptance", "OPENED", fcm.Outcome{Disposition: fcm.Accepted, Reason: fcm.Success, ProviderMessageID: "projects/fixture/messages/id"}, 1, time.Hour, "OPENED", true, "", 0},
		{"early opened failure", "OPENED", fcm.Outcome{Disposition: fcm.Rejected, Reason: fcm.Unregistered}, 1, time.Hour, "OPENED", false, "", 0},
		{"invalid token", "PENDING", fcm.Outcome{Disposition: fcm.Rejected, Reason: fcm.Unregistered}, 1, time.Hour, "FAILED", true, "unregistered", 0},
		{"local invalid payload", "PENDING", fcm.Outcome{Disposition: fcm.NotSubmitted, Reason: fcm.InvalidPayload}, 1, time.Hour, "FAILED", true, "invalid_payload", 0},
		{"uncertain", "PENDING", fcm.Outcome{Disposition: fcm.Unconfirmed, Reason: fcm.Network}, 1, time.Hour, "PENDING", false, "", 10 * time.Second},
		{"quota", "PENDING", fcm.Outcome{Disposition: fcm.Rejected, Reason: fcm.Quota}, 1, time.Hour, "PENDING", false, "", time.Minute},
		{"provider sub-microsecond floor", "PENDING", fcm.Outcome{Disposition: fcm.Rejected, Reason: fcm.Quota, RetryAt: now.Add(120*time.Second + time.Nanosecond)}, 1, time.Hour, "PENDING", false, "", 120*time.Second + time.Nanosecond},
		{"provider floor", "PENDING", fcm.Outcome{Disposition: fcm.Rejected, Reason: fcm.Quota, RetryAt: now.Add(120 * time.Second)}, 1, time.Hour, "PENDING", false, "", 120 * time.Second},
		{"last attempt", "PENDING", fcm.Outcome{Disposition: fcm.Unconfirmed, Reason: fcm.Network}, 3, time.Hour, "FAILED", true, "retry_budget_exhausted", 0},
		{"expiry", "PENDING", fcm.Outcome{Disposition: fcm.Unconfirmed, Reason: fcm.Network}, 1, -time.Second, "FAILED", true, "ttl_expired", 0},
		{"late confirmed acceptance", "PENDING", fcm.Outcome{Disposition: fcm.Accepted, Reason: fcm.Success, ProviderMessageID: "projects/fixture/messages/id"}, 1, -time.Second, "SENT", true, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := sqlc.LockResultDeliveryRow{Status: tc.status, AttemptCount: tc.count, RecordedAt: resultTime(now), DeadlineAt: resultTime(now.Add(tc.deadline)), NextAttemptAt: resultTime(now.Add(10 * time.Second))}
			desired, changed := resultMutation(row, tc.out, 3)
			if desired.Status != tc.want || changed != tc.changed || desired.TerminalReason.String != tc.reason {
				t.Fatalf("unexpected state: %s reason=%s changed=%v", desired.Status, desired.TerminalReason.String, changed)
			}
			if tc.floor > 0 && desired.NextAttemptAt.Time.Before(now.Add(tc.floor)) {
				t.Fatal("retry floor shortened")
			}
		})
	}
}
