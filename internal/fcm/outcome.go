package fcm

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"firebase.google.com/go/v4/errorutils"
	"firebase.google.com/go/v4/messaging"
)

// Disposition distinguishes provider acceptance, rejection and uncertainty.
type Disposition string

const (
	Accepted     Disposition = "accepted"
	Rejected     Disposition = "rejected"
	Unconfirmed  Disposition = "unconfirmed"
	NotSubmitted Disposition = "not_submitted"
)

// Reason is a bounded allowlisted operational code, never raw provider text.
type Reason string

const (
	AuthorizationFailure Reason = "execution_authorization"
	Success              Reason = "accepted"
	Unregistered         Reason = "unregistered"
	InvalidPayload       Reason = "invalid_payload"
	SenderMismatch       Reason = "sender_mismatch"
	CredentialFailure    Reason = "credentials"
	APNsCredentials      Reason = "apns_credentials"
	Quota                Reason = "quota"
	Unavailable          Reason = "unavailable"
	Internal             Reason = "provider_internal"
	Unknown              Reason = "unknown"
	Network              Reason = "transport_uncertain"
	InvalidResponse      Reason = "invalid_response"
	Closed               Reason = "closed"
	PermitUsed           Reason = "permit_used"
	Cancelled            Reason = "cancelled"
)

// Outcome contains safe normalized evidence. PhysicalRequests counts attempted
// base RoundTrip invocations, not proof of provider receipt/device delivery.
// RetryAt at/after deadline means no further send, not a shortened provider floor.
type Outcome struct {
	Disposition       Disposition
	Reason            Reason
	ProviderMessageID string
	PhysicalRequests  int
	RetryAt           time.Time
}

func (o Outcome) retryable() bool {
	return o.Disposition == Unconfirmed || o.Disposition == Rejected && (o.Reason == Quota || o.Reason == Unavailable || o.Reason == Internal)
}
func classify(name string, sdkErr error, record callSnapshot, project string, deadline time.Time) Outcome {
	out := Outcome{Disposition: Unconfirmed, Reason: Unknown, PhysicalRequests: record.calls}
	now := time.Now()
	if !record.at.IsZero() {
		now = record.at
	}
	if record.calls == 0 {
		reason := record.reason
		if reason == "" {
			reason = Network
		}
		return Outcome{Disposition: NotSubmitted, Reason: reason}
	}
	retry := func(reason Reason, disposition Disposition, floor time.Duration) Outcome {
		out.Reason = reason
		out.Disposition = disposition
		out.RetryAt = retryAt(record.retryAfter, now, floor, deadline)
		return out
	}
	// The status alone proves quota rejection even when the body is unusable.
	if record.status == http.StatusTooManyRequests {
		return retry(Quota, Rejected, time.Minute)
	}
	if record.bad || !record.complete {
		return retry(Network, Unconfirmed, 10*time.Second)
	}
	if sdkErr == nil && record.status >= 200 && record.status < 300 {
		prefix := "projects/" + project + "/messages/"
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) && len(name) <= 1024 && opaqueToken(name) && !strings.ContainsAny(strings.TrimPrefix(name, prefix), "/?#") {
			out.Disposition = Accepted
			out.Reason = Success
			out.ProviderMessageID = name
			return out
		}
		return retry(InvalidResponse, Unconfirmed, 10*time.Second)
	}
	out.Disposition = Rejected
	switch {
	case record.status == 404 && messaging.IsUnregistered(sdkErr):
		out.Reason = Unregistered
	case messaging.IsSenderIDMismatch(sdkErr):
		out.Reason = SenderMismatch
	case messaging.IsThirdPartyAuthError(sdkErr):
		out.Reason = APNsCredentials
	case record.status == 401 || record.status == 403 || errorutils.IsUnauthenticated(sdkErr) || errorutils.IsPermissionDenied(sdkErr):
		out.Reason = CredentialFailure
	case record.status == 400 || messaging.IsInvalidArgument(sdkErr):
		out.Reason = InvalidPayload
	case record.status == 429 || messaging.IsQuotaExceeded(sdkErr):
		return retry(Quota, Rejected, time.Minute)
	case record.status == 503:
		return retry(Unavailable, Rejected, 10*time.Second)
	case record.status >= 500:
		return retry(Internal, Rejected, 10*time.Second)
	case record.status == 404:
		out.Reason = CredentialFailure
	default:
		return retry(Unknown, Unconfirmed, 10*time.Second)
	}
	return out
}
func retryAt(header string, now time.Time, floor time.Duration, deadline time.Time) time.Time {
	next := now.Add(floor)
	header = strings.TrimSpace(header)
	if header != "" {
		digits := true
		for _, b := range []byte(header) {
			if b < '0' || b > '9' {
				digits = false
				break
			}
		}
		if digits {
			seconds, err := strconv.ParseUint(header, 10, 64)
			if err != nil || deadline.Sub(now) <= 0 || seconds > uint64(deadline.Sub(now)/time.Second) {
				return deadline
			}
			if hint := now.Add(time.Duration(seconds) * time.Second); hint.After(next) {
				next = hint
			}
		} else if hint, err := http.ParseTime(header); err == nil && hint.After(next) {
			next = hint
		}
	}
	if !next.Before(deadline) {
		return deadline
	}
	return next
}
