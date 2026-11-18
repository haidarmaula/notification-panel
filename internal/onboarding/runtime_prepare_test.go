package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type preparationGuard struct {
	checks int
	denyAt int
}

func (g *preparationGuard) Check(context.Context, RuntimeDeliveryPlan) error {
	g.checks++
	if g.denyAt > 0 && g.checks >= g.denyAt {
		return ErrRuntimeCapacityExceeded
	}
	return nil
}

func preparationPlan(t *testing.T) RuntimePlan {
	t.Helper()
	p, err := ParseRuntimePolicy([]byte(validRuntimePolicy))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.BuildRuntimePlan("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", EnvironmentStaging, 7)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

type preparationEngine struct {
	config     *stoppedRuntimeConfig
	name       string
	creates    int
	requests   int
	loseCreate bool
	conflict   bool
	drift      bool
	running    bool
}

func (e *preparationEngine) serve(w http.ResponseWriter, r *http.Request) {
	e.requests++
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1.45/containers/create":
		e.creates++
		var config stoppedRuntimeConfig
		if json.NewDecoder(r.Body).Decode(&config) != nil {
			w.WriteHeader(400)
			return
		}
		e.config = &config
		e.name = r.URL.Query().Get("name")
		if e.loseCreate {
			w.WriteHeader(500)
			return
		}
		if e.conflict {
			w.WriteHeader(409)
			return
		}
		w.WriteHeader(201)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/json"):
		if e.config == nil {
			w.WriteHeader(404)
			return
		}
		if r.URL.Path != "/v1.45/containers/"+e.name+"/json" {
			w.WriteHeader(404)
			return
		}
		host := e.config.HostConfig
		if e.drift {
			host.Binds = []string{"/:/host"}
		}
		state := map[string]any{"Status": "created", "Running": false}
		if e.running {
			state = map[string]any{"Status": "running", "Running": true}
		}
		json.NewEncoder(w).Encode(map[string]any{"Name": "/" + e.name, "Config": e.config, "HostConfig": host, "State": state, "Mounts": []any{}})
	default:
		w.WriteHeader(405)
	}
}

func testPreparer(t *testing.T, e *preparationEngine, g RuntimeReservationGuard) *RuntimePreparer {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(e.serve))
	t.Cleanup(server.Close)
	transport := &http.Transport{DialContext: server.Client().Transport.(*http.Transport).DialContext}
	// Route the fixed production origin to this fixture without adding an
	// operator URL option to the production constructor.
	fixtureURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport.Proxy = http.ProxyURL(fixtureURL)
	t.Cleanup(transport.CloseIdleConnections)
	return &RuntimePreparer{client: &http.Client{Transport: transport}, guard: g}
}

func TestRuntimePrepareStoppedEnvelopeAndExactReplay(t *testing.T) {
	engine := &preparationEngine{}
	guard := &preparationGuard{}
	p := testPreparer(t, engine, guard)
	name, err := p.Prepare(context.Background(), preparationPlan(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Prepare(context.Background(), preparationPlan(t)); err != nil {
		t.Fatal(err)
	}
	if engine.creates != 1 || !strings.HasPrefix(name, "hello-onboarding-") {
		t.Fatalf("creates=%d name=%s", engine.creates, name)
	}
	if engine.config.HostConfig.NetworkMode != "none" || engine.config.Entrypoint[0] != "/worker" || len(engine.config.HostConfig.Binds) != 0 {
		t.Fatal("unsafe stopped preparation")
	}
	// A different app receives a different deterministic allocation and never
	// stops, deletes or mutates the existing one.
	other := preparationPlan(t)
	other.delivery.ApplicationID = "33333333-3333-4333-8333-333333333333"
	otherName, err := p.Prepare(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	if otherName == name || engine.creates != 2 {
		t.Fatal("application allocation collided")
	}
}

func TestRuntimePrepareLostResponseReconcilesWithoutDuplicateCreate(t *testing.T) {
	engine := &preparationEngine{loseCreate: true}
	p := testPreparer(t, engine, &preparationGuard{})
	if _, err := p.Prepare(context.Background(), preparationPlan(t)); !errors.Is(err, ErrRuntimePreparationUnavailable) {
		t.Fatal(err)
	}
	engine.loseCreate = false
	if _, err := p.Prepare(context.Background(), preparationPlan(t)); err != nil {
		t.Fatal(err)
	}
	if engine.creates != 1 {
		t.Fatal("lost response created a duplicate")
	}
}

func TestRuntimePrepareRejectsDriftAndRunningAllocation(t *testing.T) {
	for _, running := range []bool{false, true} {
		engine := &preparationEngine{}
		p := testPreparer(t, engine, &preparationGuard{})
		if _, err := p.Prepare(context.Background(), preparationPlan(t)); err != nil {
			t.Fatal(err)
		}
		engine.running = running
		engine.drift = !running
		if _, err := p.Prepare(context.Background(), preparationPlan(t)); !errors.Is(err, ErrRuntimePreparationDrift) {
			t.Fatal(err)
		}
		if engine.creates != 1 {
			t.Fatal("drift triggered mutation")
		}
	}
}

func TestRuntimePrepareRequiresReservationAndCancellation(t *testing.T) {
	for _, denyAt := range []int{1, 2} {
		engine := &preparationEngine{}
		p := testPreparer(t, engine, &preparationGuard{denyAt: denyAt})
		if _, err := p.Prepare(context.Background(), preparationPlan(t)); !errors.Is(err, ErrRuntimeCapacityExceeded) {
			t.Fatal(err)
		}
		if engine.creates != 0 {
			t.Fatal("created without reservation")
		}
	}
	engine := &preparationEngine{}
	p := testPreparer(t, engine, nil)
	if _, err := p.Prepare(context.Background(), preparationPlan(t)); !errors.Is(err, ErrInvalidRuntimePlan) || engine.requests != 0 {
		t.Fatal("missing guard contacted engine")
	}
	p.guard = &preparationGuard{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Prepare(ctx, preparationPlan(t)); !errors.Is(err, ErrRuntimePreparationUnavailable) {
		t.Fatal(err)
	}
	if engine.requests != 0 {
		t.Fatal("canceled request contacted engine")
	}
}

func TestRuntimePrepareReconcilesConcurrentNameConflict(t *testing.T) {
	engine := &preparationEngine{conflict: true}
	p := testPreparer(t, engine, &preparationGuard{})
	if _, err := p.Prepare(context.Background(), preparationPlan(t)); err != nil {
		t.Fatal(err)
	}
	if engine.creates != 1 {
		t.Fatal("unexpected retry")
	}
}

func TestRuntimePrepareRejectsBoundedOrMalformedInspectResponse(t *testing.T) {
	for _, body := range []string{strings.Repeat(" ", 64*1024+1), `{"State":`} {
		engine := &preparationEngine{}
		p := testPreparer(t, engine, &preparationGuard{})
		p.client.Transport = &preparationResponse{body: body}
		if _, err := p.Prepare(context.Background(), preparationPlan(t)); !errors.Is(err, ErrRuntimePreparationUnavailable) {
			t.Fatal(err)
		}
	}
}

type preparationResponse struct{ body string }

func (r *preparationResponse) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(r.body)), Header: make(http.Header)}, nil
}
