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

func TestDecodeScopedDeliveryRequiresCompleteUnambiguousProjection(t *testing.T) {
	id := "01000000-0000-0000-0000-000000000000"
	event := ScopedDeliveryUpdated{SchemaVersion: 2, ApplicationID: id, OriginEpoch: id, SourceEpoch: id, EventID: id, DeliveryID: id, DeliveryVersion: 1, EventKind: "submission", NotificationID: 12, UserID: 2, Provider: "FCM", Status: "PENDING", OccurredAt: time.Unix(2000000000, 0).UTC()}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeScopedDelivery(raw); err != nil {
		t.Fatal(err)
	}
	if string(DeliveryUpdateKey(12)) != "delivery_12" {
		t.Fatal("Kafka key changed")
	}
	for _, name := range []string{"missing nullable", "null required", "unknown", "case alias", "duplicate", "trailing", "oversized"} {
		t.Run(name, func(t *testing.T) {
			var fields map[string]any
			if json.Unmarshal(raw, &fields) != nil {
				t.Fatal("fixture decode")
			}
			switch name {
			case "missing nullable":
				delete(fields, "reason")
			case "null required":
				fields["user_id"] = nil
			case "unknown":
				fields["address"] = "sensitive"
			case "case alias":
				fields["Status"] = fields["status"]
				delete(fields, "status")
			}
			data, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "duplicate":
				data = append([]byte(`{"status":"PENDING",`), raw[1:]...)
			case "trailing":
				data = append(data, []byte(` {}`)...)
			case "oversized":
				data = make([]byte, 65537)
			}
			if _, err := DecodeScopedDelivery(data); err != ErrScopedDeliveryEnvelope {
				t.Fatal("ambiguous projection accepted")
			}
		})
	}
}
