// Package pushpayload builds per-device FCM messages from committed identity and
// trusted routes. It performs no provider I/O or database authorization.
package pushpayload

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"firebase.google.com/go/v4/messaging"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/installation"
	"hello/internal/receipt"
)

// ErrPayload contains no address, capability or user-provided payload details.
var ErrPayload = errors.New("invalid scoped push payload")

// Route is trusted deployment policy for one application/binding route. Firebase
// credentials and project selection must be pinned by the later transport too.
type Route struct {
	ApplicationID pgtype.UUID
	Environment   string
	Namespace     string
	Key           string
	Platform      string
	AddressType   string
}

// Destination comes from the locked immutable binding; its address is sensitive.
// Never log the whole destination, request or resulting SDK message.
type Destination struct {
	BindingID      int64
	InstallationID pgtype.UUID
	OwnerEpoch     int64
	ApplicationID  pgtype.UUID
	Namespace      string
	RouteKey       string
	Platform       string
	AddressType    string
	Address        string
}

// Request uses persisted identity, expiry and accepted deadline. Custom string
// data cannot replace contract fields. The caller owns inputs during Build.
type Request struct {
	Scope       receipt.Scope
	Capability  receipt.Capability
	Destination Destination
	Deadline    time.Time
	Title       string
	Body        string
	Data        map[string]string
}

// Builder retains an immutable route and is safe for concurrent independent builds.
type Builder struct{ route Route }

// New validates explicit policy; unknown platforms/address types fail closed.
func New(route Route) (*Builder, error) {
	if !route.ApplicationID.Valid || route.ApplicationID.Bytes == [16]byte{} || !environmentLabel(route.Environment) ||
		!metadata(route.Namespace, 255) || !metadata(route.Key, 255) || (route.Platform != "ANDROID" && route.Platform != "IOS") ||
		(route.AddressType != installation.FCMFID && route.AddressType != installation.FCMRegistrationToken) {
		return nil, ErrPayload
	}
	return &Builder{route: route}, nil
}

// Build returns a caller-owned SDK message with one target and a conservative
// 4096-byte encoded payload budget excluding target address. It rejects expired
// work and never extends deadline through provider TTL rounding. The later
// executor must still recheck lease/route/current owner immediately before send.
func (b *Builder) Build(now time.Time, request Request) (*messaging.Message, error) {
	if b == nil || now.IsZero() || !request.Deadline.After(now) || request.Deadline.Sub(now) > 28*24*time.Hour ||
		request.Scope.ApplicationID != b.route.ApplicationID || !request.Capability.Matches(b.route.Environment, request.Scope) ||
		time.Unix(request.Scope.ExpiresUnix, 0).Before(request.Deadline) || !b.matches(request.Destination) || request.Destination.BindingID != request.Scope.BindingID ||
		request.Destination.InstallationID != request.Scope.InstallationID || request.Destination.OwnerEpoch != request.Scope.OwnerEpoch ||
		!metadata(request.Destination.Address, 8192) || !utf8.ValidString(request.Title) || !utf8.ValidString(request.Body) ||
		(request.Title == "" && request.Body == "") || len(request.Title) > 4096 || len(request.Body) > 4096 || len(request.Data) > 100 {
		return nil, ErrPayload
	}
	data := map[string]string{
		"schema_version": "1", "application_id": request.Scope.ApplicationID.String(),
		"notification_id": strconv.FormatInt(request.Scope.NotificationID, 10), "delivery_id": request.Scope.DeliveryID.String(),
		"registration_id": strconv.FormatInt(request.Scope.BindingID, 10), "owner_epoch": strconv.FormatInt(request.Scope.OwnerEpoch, 10),
		"receipt_token": request.Capability.WireValue(),
	}
	for key, value := range request.Data {
		if _, reserved := data[key]; reserved || !dataKey(key) || !utf8.ValidString(value) || len(value) > 4096 {
			return nil, ErrPayload
		}
		data[key] = value
	}
	message := &messaging.Message{Data: data, Notification: &messaging.Notification{Title: request.Title, Body: request.Body}}
	if b.route.Platform == "ANDROID" {
		ttl := request.Deadline.Sub(now).Truncate(time.Second)
		message.Android = &messaging.AndroidConfig{TTL: &ttl}
	} else {
		message.APNS = &messaging.APNSConfig{Headers: map[string]string{"apns-expiration": strconv.FormatInt(request.Deadline.Unix(), 10)}}
	}
	encoded, err := json.Marshal(message)
	if err != nil || len(encoded) > 4096 {
		return nil, ErrPayload
	}
	if b.route.AddressType == installation.FCMFID {
		message.Fid = request.Destination.Address
	} else {
		message.Token = request.Destination.Address
	}
	return message, nil
}
func (b *Builder) matches(destination Destination) bool {
	return destination.ApplicationID == b.route.ApplicationID && destination.Namespace == b.route.Namespace &&
		destination.RouteKey == b.route.Key && destination.Platform == b.route.Platform && destination.AddressType == b.route.AddressType
}
func dataKey(key string) bool {
	if !metadata(key, 255) {
		return false
	}
	return key != "from" && key != "message_type" && !strings.HasPrefix(key, "google") && !strings.HasPrefix(key, "gcm")
}
func metadata(value string, limit int) bool {
	return len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}
func environmentLabel(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, b := range []byte(value) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}
