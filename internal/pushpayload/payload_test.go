package pushpayload

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/installation"
	"hello/internal/receipt"
)

func fixture(t *testing.T, platform, addressType string) (*Builder, Request, time.Time) {
	t.Helper()
	now := time.Unix(1900000000, 0)
	id := func(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }
	scope := receipt.Scope{ApplicationID: id(1), OriginEpoch: id(2), NotificationID: 3, DeliveryID: id(4), BindingID: 5, InstallationID: id(6), OwnerEpoch: 7, ExpiresUnix: now.Add(24 * time.Hour).Unix()}
	signer, err := receipt.NewSigner("test", map[string]ed25519.PrivateKey{"k": ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := signer.Sign("k", scope)
	if err != nil {
		t.Fatal(err)
	}
	route := Route{ApplicationID: scope.ApplicationID, Environment: "test", Namespace: "firebase-fixture", Key: "mobile-route", Platform: platform, AddressType: addressType}
	builder, err := New(route)
	if err != nil {
		t.Fatal(err)
	}
	return builder, Request{Scope: scope, Capability: capability, Destination: Destination{BindingID: scope.BindingID, InstallationID: scope.InstallationID, OwnerEpoch: scope.OwnerEpoch, ApplicationID: scope.ApplicationID, Namespace: route.Namespace, RouteKey: route.Key, Platform: platform, AddressType: addressType, Address: "fixture-address"}, Deadline: now.Add(time.Hour), Title: "Fixture", Body: "Body", Data: map[string]string{"screen": "home"}}, now
}
func TestBuildPlatformAndAddressModes(t *testing.T) {
	for _, platform := range []string{"ANDROID", "IOS"} {
		for _, addressType := range []string{installation.FCMFID, installation.FCMRegistrationToken} {
			t.Run(platform+"/"+addressType, func(t *testing.T) {
				builder, request, now := fixture(t, platform, addressType)
				message, err := builder.Build(now, request)
				if err != nil {
					t.Fatal(err)
				}
				if addressType == installation.FCMFID {
					if message.Fid != request.Destination.Address || message.Token != "" {
						t.Fatal("wrong FID target")
					}
				} else {
					if message.Token != request.Destination.Address || message.Fid != "" {
						t.Fatal("wrong registration-token target")
					}
				}
				if message.Data["receipt_token"] != request.Capability.WireValue() || message.Data["schema_version"] != "1" || message.Data["screen"] != "home" {
					t.Fatal("missing payload contract")
				}
				request.Data["screen"] = "changed"
				if message.Data["screen"] != "home" {
					t.Fatal("aliased input data")
				}
				if platform == "ANDROID" {
					if message.Android == nil || *message.Android.TTL != time.Hour || message.APNS != nil {
						t.Fatal("wrong Android expiration")
					}
				} else {
					if message.APNS == nil || message.Android != nil || message.APNS.Headers["apns-expiration"] != "1900003600" {
						t.Fatal("wrong APNs expiration")
					}
				}
			})
		}
	}
}

func TestBuildRejectsScopeRouteAndPayloadConfusion(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Request)
	}{
		{"application", func(r *Request) { r.Destination.ApplicationID.Bytes[0]++ }},
		{"binding", func(r *Request) { r.Destination.BindingID++ }},
		{"installation", func(r *Request) { r.Destination.InstallationID.Bytes[0]++ }},
		{"owner epoch", func(r *Request) { r.Destination.OwnerEpoch++ }},
		{"namespace", func(r *Request) { r.Destination.Namespace = "other-project" }},
		{"route", func(r *Request) { r.Destination.RouteKey = "other-route" }},
		{"platform", func(r *Request) { r.Destination.Platform = "IOS" }},
		{"address type", func(r *Request) { r.Destination.AddressType = "APNS" }},
		{"zero capability", func(r *Request) { r.Capability = receipt.Capability{} }},
		{"scope changed", func(r *Request) { r.Scope.OwnerEpoch++ }},
		{"expiry changed", func(r *Request) { r.Scope.ExpiresUnix++ }},
		{"deadline", func(r *Request) { r.Deadline = time.Unix(1900000000, 0) }},
		{"too long", func(r *Request) { r.Deadline = r.Deadline.Add(29 * 24 * time.Hour) }},
		{"capability too short", func(r *Request) { r.Deadline = time.Unix(r.Scope.ExpiresUnix+1, 0) }},
		{"address", func(r *Request) { r.Destination.Address = "" }},
		{"UTF8", func(r *Request) { r.Body = string([]byte{0xff}) }},
		{"empty text", func(r *Request) { r.Title = ""; r.Body = "" }},
		{"encoded overhead", func(r *Request) { r.Body = strings.Repeat("a", 4096) }},
		{"contract collision", func(r *Request) { r.Data["receipt_token"] = "forged" }},
		{"FCM reserved from", func(r *Request) { r.Data["from"] = "forged" }},
		{"FCM reserved google", func(r *Request) { r.Data["google.test"] = "forged" }},
		{"FCM reserved gcm", func(r *Request) { r.Data["gcm.notification.title"] = "forged" }},
		{"too many fields", func(r *Request) {
			for i := 0; i < 101; i++ {
				r.Data[fmt.Sprint(i)] = "v"
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			builder, request, now := fixture(t, "ANDROID", installation.FCMRegistrationToken)
			tt.change(&request)
			if message, err := builder.Build(now, request); message != nil || !errors.Is(err, ErrPayload) {
				t.Fatal("unsafe request accepted")
			}
		})
	}
}

func TestBuildDeadlineRoundingAndRepeatedPayload(t *testing.T) {
	for _, platform := range []string{"ANDROID", "IOS"} {
		builder, request, now := fixture(t, platform, installation.FCMRegistrationToken)
		first, err := builder.Build(now, request)
		if err != nil {
			t.Fatal(err)
		}
		second, err := builder.Build(now.Add(1500*time.Millisecond), request)
		if err != nil {
			t.Fatal(err)
		}
		if first.Data["receipt_token"] != second.Data["receipt_token"] || first.Data["delivery_id"] != second.Data["delivery_id"] {
			t.Fatal("retry changed capability or delivery identity")
		}
		if platform == "ANDROID" && *second.Android.TTL != 3598*time.Second {
			t.Fatal("retry extended freshness")
		}
		request.Deadline = now.Add(500 * time.Millisecond)
		final, err := builder.Build(now, request)
		if err != nil {
			t.Fatal(err)
		}
		if platform == "ANDROID" && *final.Android.TTL != 0 {
			t.Fatal("subsecond lifetime extended")
		}
		if platform == "IOS" && final.APNS.Headers["apns-expiration"] != "1900000000" {
			t.Fatal("APNs lifetime extended")
		}
		if message, err := builder.Build(request.Deadline, request); message != nil || !errors.Is(err, ErrPayload) {
			t.Fatal("send at deadline accepted")
		}
	}
}

func TestBuilderPinsConfigurationAndRejectsInvalidRoutes(t *testing.T) {
	builder, request, now := fixture(t, "ANDROID", installation.FCMFID)
	route := builder.route
	for _, change := range []func(*Route){func(r *Route) { r.ApplicationID = pgtype.UUID{} }, func(r *Route) { r.Environment = "" }, func(r *Route) { r.Namespace = "" }, func(r *Route) { r.Key = "route\n" }, func(r *Route) { r.Platform = "WEB" }, func(r *Route) { r.AddressType = "ONESIGNAL" }} {
		changed := route
		change(&changed)
		if b, err := New(changed); b != nil || !errors.Is(err, ErrPayload) {
			t.Fatal("invalid route accepted")
		}
	}
	route.Environment = "production"
	other, err := New(route)
	if err != nil {
		t.Fatal(err)
	}
	if m, err := other.Build(now, request); m != nil || !errors.Is(err, ErrPayload) {
		t.Fatal("foreign environment capability accepted")
	}
	if m, err := new(Builder).Build(now, request); m != nil || !errors.Is(err, ErrPayload) {
		t.Fatal("zero builder accepted request")
	}
	message, err := builder.Build(now, request)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"token"`)) || !bytes.Contains(encoded, []byte(`"fid"`)) {
		t.Fatal("serialized target contract changed")
	}
}
