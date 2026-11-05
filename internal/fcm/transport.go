package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var errTransport = errors.New("FCM transport boundary rejected")

type callKey struct{}
type callSnapshot struct {
	calls, status int
	reason        Reason
	complete, bad bool
	retryAfter    string
	at            time.Time
}
type callRecord struct {
	mu            sync.Mutex
	once          atomic.Bool
	cancel        context.CancelFunc
	token         string
	deadline      time.Time
	check         func(context.Context) (time.Time, error)
	snapshotValue callSnapshot
}

func (r *callRecord) String() string { return "[REDACTED]" }
func (r *callRecord) snapshot() callSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotValue
}

type guardTransport struct {
	client *clientState
	base   http.RoundTripper
}

func (t *guardTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r, ok := request.Context().Value(callKey{}).(*callRecord)
	if !ok || r == nil || request.Method != http.MethodPost || request.URL.String() != t.client.target || !r.once.CompareAndSwap(false, true) {
		if ok && r != nil {
			r.cancel()
		}
		return nil, errTransport
	}
	if err := t.client.physical.wait(request.Context()); err != nil {
		r.cancel()
		return nil, errTransport
	}
	if request.Context().Err() != nil || !r.deadline.After(time.Now()) {
		r.cancel()
		return nil, errTransport
	}
	physicalContext := request.Context()
	validUntil := r.deadline
	cancelWindow := func() {}
	if r.check != nil {
		until, err := r.check(physicalContext)
		if err != nil || !until.After(time.Now()) {
			r.mu.Lock()
			r.snapshotValue.reason = AuthorizationFailure
			r.mu.Unlock()
			r.cancel()
			return nil, errTransport
		}
		if until.Before(validUntil) {
			validUntil = until
		}
		physicalContext, cancelWindow = context.WithDeadline(physicalContext, validUntil)
	}
	if physicalContext.Err() != nil || !r.deadline.After(time.Now()) {
		cancelWindow()
		r.cancel()
		return nil, errTransport
	}
	// Retime after local pacing, not before it. Network transit/provider retention
	// still cannot promise device display before a wall-clock deadline.
	body, err := retime(request.Body, r.deadline)
	if err != nil {
		cancelWindow()
		r.cancel()
		return nil, errTransport
	}
	if physicalContext.Err() != nil || !validUntil.After(time.Now()) {
		cancelWindow()
		r.cancel()
		return nil, errTransport
	}
	cloned := request.Clone(physicalContext)
	cloned.Body = io.NopCloser(bytes.NewReader(body))
	cloned.ContentLength = int64(len(body))
	// Disable Go's own replay of a non-idempotent POST, including zero-write retry.
	cloned.GetBody = nil
	cloned.Header.Del("Idempotency-Key")
	cloned.Header.Del("X-Idempotency-Key")
	cloned.Header.Set("Authorization", "Bearer "+r.token)
	r.mu.Lock()
	r.snapshotValue.calls++
	r.mu.Unlock()
	response, err := t.base.RoundTrip(cloned)
	if err != nil || response == nil {
		cancelWindow()
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		r.cancel()
		return nil, errTransport
	}
	r.mu.Lock()
	r.snapshotValue.status = response.StatusCode
	r.snapshotValue.retryAfter = response.Header.Get("Retry-After")
	r.snapshotValue.at = time.Now()
	r.mu.Unlock()
	if response.Body == nil {
		response.Body = http.NoBody
	}
	response.Body = &boundedResponse{original: response.Body, reader: io.LimitReader(response.Body, 64*1024+1), record: r, status: response.StatusCode, cancel: cancelWindow}
	return response, nil
}
func retime(body io.ReadCloser, deadline time.Time) ([]byte, error) {
	if body == nil {
		return nil, errTransport
	}
	data, err := io.ReadAll(io.LimitReader(body, 32*1024+1))
	closeErr := body.Close()
	if err != nil || closeErr != nil || len(data) > 32*1024 {
		return nil, errTransport
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil {
		return nil, errTransport
	}
	var message map[string]json.RawMessage
	if json.Unmarshal(envelope["message"], &message) != nil || message == nil {
		return nil, errTransport
	}
	if raw, ok := message["android"]; ok {
		var android map[string]json.RawMessage
		if json.Unmarshal(raw, &android) != nil || android == nil {
			return nil, errTransport
		}
		remaining := deadline.Sub(time.Now())
		if remaining <= 0 {
			return nil, errTransport
		}
		ttl, _ := json.Marshal(strconv.FormatInt(int64(remaining/time.Second), 10) + "s")
		android["ttl"] = ttl
		message["android"], err = json.Marshal(android)
		if err != nil {
			return nil, errTransport
		}
		envelope["message"], err = json.Marshal(message)
		if err != nil {
			return nil, errTransport
		}
		return json.Marshal(envelope)
	}
	return data, nil
}

type boundedResponse struct {
	original      io.ReadCloser
	reader        io.Reader
	record        *callRecord
	status, total int
	cancel        context.CancelFunc
}

func (b *boundedResponse) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.total += n
	if b.total > 64*1024 {
		err = errTransport
	}
	if err != nil {
		b.record.mu.Lock()
		if err == io.EOF {
			b.record.snapshotValue.complete = true
		} else {
			b.record.snapshotValue.bad = true
		}
		b.record.mu.Unlock()
		if err != io.EOF || b.status == http.StatusServiceUnavailable {
			b.record.cancel()
		}
	}
	return n, err
}
func (b *boundedResponse) Close() error { defer b.cancel(); return b.original.Close() }
