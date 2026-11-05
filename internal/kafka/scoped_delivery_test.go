package kafka

import (
	"encoding/json"
	"testing"
	"time"
)

func TestScopedDeliveryFullProjection(t *testing.T) {
	id := "01000000-0000-0000-0000-000000000000"
	at := time.Unix(2000000000, 123000).UTC()
	message := "projects/fixture/messages/id"
	event := ScopedDeliveryUpdated{SchemaVersion: 2, ApplicationID: id, OriginEpoch: id, SourceEpoch: id, EventID: id, DeliveryID: id, DeliveryVersion: 1, EventKind: "submission", NotificationID: 1, UserID: 2, Provider: "FCM", Status: "SENT", ProviderMessageID: &message, ProviderAcceptedAt: &at, OccurredAt: at}
	if event.Validate() != nil {
		t.Fatal("valid projection rejected")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip ScopedDeliveryUpdated
	if json.Unmarshal(raw, &roundtrip) != nil || roundtrip.Validate() != nil {
		t.Fatal("projection does not round trip")
	}
	for _, name := range []string{"epoch", "version", "evidence pair", "opened flag", "timestamp", "reason"} {
		t.Run(name, func(t *testing.T) {
			bad := event
			switch name {
			case "epoch":
				bad.SourceEpoch = "untrusted"
			case "version":
				bad.DeliveryVersion = 0
			case "evidence pair":
				bad.ProviderAcceptedAt = nil
			case "opened flag":
				bad.ObservedOpened = true
			case "timestamp":
				bad.OccurredAt = at.Add(time.Nanosecond)
			case "reason":
				reason := "raw error with token"
				bad.Reason = &reason
			}
			if bad.Validate() == nil {
				t.Fatal("invalid projection accepted")
			}
		})
	}
}
