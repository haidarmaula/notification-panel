package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type preflightEngine struct {
	name, id              string
	config                stoppedRuntimeConfig
	state                 string
	creates, starts, logs int
	startedBy             string
	lostResponse          string
	writableMount         bool
	output                []byte
}

func (e *preflightEngine) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	const prefix = "/v1.45/containers/"
	path := strings.TrimPrefix(request.URL.Path, prefix)
	switch {
	case request.Method == http.MethodPost && path == "create":
		e.creates++
		if json.NewDecoder(request.Body).Decode(&e.config) != nil || request.URL.Query().Get("name") != e.name {
			recorder.WriteHeader(http.StatusBadRequest)
			break
		}
		e.state = "created"
		if e.lostResponse == "create" {
			e.lostResponse = ""
			return nil, errors.New("lost Engine create response")
		}
		recorder.WriteHeader(http.StatusCreated)
	case request.Method == http.MethodGet && (path == e.name+"/json" || path == e.id+"/json"):
		if e.state == "" {
			recorder.WriteHeader(http.StatusNotFound)
			break
		}
		actual := runtimeInspection{ID: e.id, Name: "/" + e.name, Config: e.config, HostConfig: e.config.HostConfig}
		actual.State.Status = e.state
		for _, stored := range e.config.HostConfig.Mounts {
			actual.Mounts = append(actual.Mounts, struct {
				Type, Source, Destination string
				RW                        bool
			}{Type: stored.Type, Source: stored.Source, Destination: stored.Target, RW: e.writableMount})
		}
		_ = json.NewEncoder(recorder).Encode(actual)
	case request.Method == http.MethodPost && strings.HasSuffix(path, "/start"):
		e.startedBy = strings.TrimSuffix(path, "/start")
		if e.startedBy != e.id || e.state != "created" {
			recorder.WriteHeader(http.StatusConflict)
			break
		}
		e.starts++
		e.state = "exited"
		if e.lostResponse == "start" {
			e.lostResponse = ""
			return nil, errors.New("lost Engine start response")
		}
		recorder.WriteHeader(http.StatusNoContent)
	case request.Method == http.MethodGet && path == e.id+"/logs" && request.URL.Query().Get("stdout") == "1":
		e.logs++
		_, _ = recorder.Write(e.output)
	default:
		recorder.WriteHeader(http.StatusNotFound)
	}
	return recorder.Result(), nil
}

func preflightReport(applicationID string, digest [32]byte) []byte {
	data, _ := json.Marshal(map[string]string{
		"application_id": applicationID,
		"policy_sha256":  hex.EncodeToString(digest[:]),
	})
	frame := make([]byte, 8, len(data)+8)
	frame[0] = 1
	binary.BigEndian.PutUint32(frame[4:], uint32(len(data)))
	return append(frame, data...)
}

func newPreflightFixture(t *testing.T) (*preflightEngine, stoppedRuntimeConfig, string, [32]byte) {
	t.Helper()
	plan := preparationPlan(t).Delivery()
	config := stoppedRuntimeEnvelope(plan)
	name := stoppedRuntimeName(config)
	config.Cmd = []string{"--check-delivery-policy"}
	config.HostConfig.NetworkMode = plan.Network
	config.HostConfig.Mounts = []runtimeBindMount{
		{Type: "bind", Source: "/protected/policy", Target: "/etc/hello/worker.json", ReadOnly: true},
		{Type: "bind", Source: "/protected/credential", Target: "/etc/hello/fcm/service-account.json", ReadOnly: true},
	}
	digest := sha256.Sum256([]byte("worker policy fixture"))
	engine := &preflightEngine{name: name, id: strings.Repeat("a", 64), output: preflightReport(plan.ApplicationID, digest)}
	return engine, config, plan.ApplicationID, digest
}

func TestDeliveryPreflightStartsByIDAndReplaysReport(t *testing.T) {
	engine, config, applicationID, digest := newPreflightFixture(t)
	preparer := &RuntimePreparer{client: &http.Client{Transport: engine}}
	check := func() error { return nil }
	for range 2 {
		name, err := preparer.runDeliveryPreflight(t.Context(), check, applicationID, digest, config, engine.name)
		if err != nil || name != engine.name {
			t.Fatalf("preflight did not verify report: %v", err)
		}
	}
	if engine.creates != 1 || engine.starts != 1 || engine.startedBy != engine.id || engine.logs != 2 {
		t.Fatalf("creates=%d starts=%d started by=%q logs=%d", engine.creates, engine.starts, engine.startedBy, engine.logs)
	}
}

func TestDeliveryPreflightRechecksAuthorityBeforeStartingCreatedContainer(t *testing.T) {
	engine, config, applicationID, digest := newPreflightFixture(t)
	preparer := &RuntimePreparer{client: &http.Client{Transport: engine}}
	check := func() error {
		if engine.creates > 0 {
			return ErrRuntimeReservationUnavailable
		}
		return nil
	}
	_, err := preparer.runDeliveryPreflight(t.Context(), check, applicationID, digest, config, engine.name)
	if !errors.Is(err, ErrRuntimeReservationUnavailable) || engine.creates != 1 || engine.startedBy != "" || engine.logs != 0 {
		t.Fatalf("revoked preflight should retain stopped allocation without starting: %v", err)
	}
}

func TestDeliveryPreflightReconcilesAmbiguousCreateAndStart(t *testing.T) {
	for _, phase := range []string{"create", "start"} {
		t.Run(phase, func(t *testing.T) {
			engine, config, applicationID, digest := newPreflightFixture(t)
			engine.lostResponse = phase
			preparer := &RuntimePreparer{client: &http.Client{Transport: engine}}
			check := func() error { return nil }
			if _, err := preparer.runDeliveryPreflight(context.Background(), check, applicationID, digest, config, engine.name); !errors.Is(err, ErrRuntimePreparationUnavailable) {
				t.Fatalf("expected ambiguous result: %v", err)
			}
			if _, err := preparer.runDeliveryPreflight(context.Background(), check, applicationID, digest, config, engine.name); err != nil {
				t.Fatalf("unable to reconcile original allocation: %v", err)
			}
			if engine.creates != 1 || engine.starts != 1 || engine.logs != 1 {
				t.Fatalf("duplicate effect: creates=%d starts=%d logs=%d", engine.creates, engine.starts, engine.logs)
			}
		})
	}
}

func TestDeliveryPreflightRejectsUnverifiedReport(t *testing.T) {
	for _, report := range []string{"unframed", "wrong digest", "wrong application"} {
		t.Run(report, func(t *testing.T) {
			engine, config, applicationID, digest := newPreflightFixture(t)
			switch report {
			case "unframed":
				engine.output = engine.output[8:]
			case "wrong digest":
				engine.output = preflightReport(applicationID, sha256.Sum256([]byte("another policy")))
			case "wrong application":
				engine.output = preflightReport("33333333-3333-4333-8333-333333333333", digest)
			}
			preparer := &RuntimePreparer{client: &http.Client{Transport: engine}}
			_, err := preparer.runDeliveryPreflight(t.Context(), func() error { return nil }, applicationID, digest, config, engine.name)
			if !errors.Is(err, ErrRuntimePreparationUnavailable) || engine.logs != 1 {
				t.Fatalf("unverified report must fail after reading worker output: %v", err)
			}
		})
	}
}

func TestDeliveryPreflightRejectsWritableMountBeforeStarting(t *testing.T) {
	engine, config, applicationID, digest := newPreflightFixture(t)
	engine.writableMount = true
	preparer := &RuntimePreparer{client: &http.Client{Transport: engine}}
	_, err := preparer.runDeliveryPreflight(t.Context(), func() error { return nil }, applicationID, digest, config, engine.name)
	if !errors.Is(err, ErrRuntimePreparationDrift) || engine.creates != 1 || engine.startedBy != "" || engine.logs != 0 {
		t.Fatalf("writable mount should prevent worker start: %v", err)
	}
}
