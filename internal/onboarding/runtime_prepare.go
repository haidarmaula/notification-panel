package onboarding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"time"
)

var (
	ErrRuntimePreparationUnavailable = errors.New("runtime preparation unavailable")
	ErrRuntimePreparationDrift       = errors.New("runtime preparation differs from approved plan")
)

const runtimeEnginePrefix = "http://docker/v1.45/containers/"
const runtimePreparationCommand = "/worker"

// RuntimeReservationGuard rechecks a durable, exact-plan capacity reservation.
// Implementations must fail closed on stale, revoked or mismatched reservations.
type RuntimeReservationGuard interface {
	Check(context.Context, RuntimeDeliveryPlan) error
}

// RuntimePreparer creates only stopped, unconfigured Docker envelopes. It cannot
// start, delete or modify containers, publish credentials, or acknowledge readiness.
// The caller must retain a durable capacity reservation before calling Prepare;
// a timeout does not prove that Docker failed to create the container.
type RuntimePreparer struct {
	client *http.Client
	guard  RuntimeReservationGuard
}

// NewRuntimePreparer uses the protected host Docker socket exclusively. This must
// remain an operator-owned service dependency, never a dashboard/API capability.
func NewRuntimePreparer(guard RuntimeReservationGuard) *RuntimePreparer {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}, MaxConnsPerHost: 1, DisableKeepAlives: true}
	return &RuntimePreparer{guard: guard, client: &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Close releases idle Engine connections without changing any Docker allocation
// or its durable capacity reservation.
func (p *RuntimePreparer) Close() {
	if p != nil && p.client != nil {
		p.client.CloseIdleConnections()
	}
}

type stoppedRuntimeConfig struct {
	Image      string
	User       string
	Entrypoint []string
	Cmd        []string
	Env        []string
	Labels     map[string]string
	HostConfig stoppedRuntimeHost
}

type stoppedRuntimeHost struct {
	NetworkMode     string
	ReadonlyRootfs  bool
	CapDrop         []string
	CapAdd          []string
	SecurityOpt     []string
	Memory          int64
	NanoCpus        int64
	PidsLimit       int32
	Privileged      bool
	Binds           []string
	Mounts          []json.RawMessage
	Devices         []json.RawMessage
	DeviceRequests  []json.RawMessage
	PidMode         string
	IpcMode         string
	UTSMode         string
	UsernsMode      string
	PortBindings    map[string]json.RawMessage
	PublishAllPorts bool
	VolumesFrom     []string
	RestartPolicy   struct {
		Name              string
		MaximumRetryCount int
	}
}

// Prepare creates or reconciles one exact stopped envelope. The returned name
// identifies a Docker allocation only; it is not activation or delivery evidence.
// No credentials or runtime policy are mounted until a separate approved adapter
// implements the complete platform profile. The real worker entrypoint is fixed;
// this adapter exposes no start operation and installs no usable runtime policy.
func (p *RuntimePreparer) Prepare(ctx context.Context, plan RuntimePlan) (string, error) {
	d := plan.Delivery()
	if p == nil || p.client == nil || p.guard == nil || !validRuntimeUUID(d.ApplicationID) || !validRuntimeUUID(d.ConfigurationID) ||
		d.Generation <= 0 || d.PolicyRevision <= 0 || !validRuntimeImage(d.ImageDigest) || !validRuntimeNetwork(d.Network) ||
		d.UID != 65532 || d.GID != 65532 || !d.ReadOnlyRoot || !d.DropAllCapabilities || !d.NoNewPrivileges ||
		d.MemoryBytes < runtimeDeliveryMinMemory || d.MemoryBytes > runtimeDeliveryMaxMemory ||
		d.NanoCPUs < 100_000_000 || d.NanoCPUs > 4_000_000_000 || d.PIDsLimit < 16 || d.PIDsLimit > 512 {
		return "", ErrInvalidRuntimePlan
	}
	if err := p.guard.Check(ctx, d); err != nil {
		return "", err
	}
	config := stoppedRuntimeConfig{
		Image: d.ImageDigest, User: "65532:65532", Entrypoint: []string{runtimePreparationCommand}, Cmd: []string{}, Env: []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		Labels: map[string]string{"hello.onboarding.application": d.ApplicationID, "hello.onboarding.configuration": d.ConfigurationID,
			"hello.onboarding.generation": strconv.FormatInt(d.Generation, 10), "hello.onboarding.profile": strconv.FormatInt(d.PolicyRevision, 10),
			"hello.onboarding.environment": string(d.Environment), "hello.onboarding.state": "stopped-unconfigured"},
		HostConfig: stoppedRuntimeHost{NetworkMode: "none", ReadonlyRootfs: true, CapDrop: []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges"}, Memory: d.MemoryBytes, NanoCpus: d.NanoCPUs, PidsLimit: d.PIDsLimit},
	}
	config.HostConfig.RestartPolicy.Name = "no"
	encoded, _ := json.Marshal(config)
	digest := sha256.Sum256(encoded)
	name := "hello-onboarding-" + hex.EncodeToString(digest[:])
	exists, err := p.inspect(ctx, name, config)
	if err != nil || exists {
		return name, err
	}
	if err := p.guard.Check(ctx, d); err != nil {
		return name, err
	}
	response, err := p.request(ctx, http.MethodPost, "create?name="+name, encoded)
	if err != nil {
		return name, err
	}
	status := response.StatusCode
	response.Body.Close()
	if status != http.StatusCreated && status != http.StatusConflict {
		return name, ErrRuntimePreparationUnavailable
	}
	// Inspect also recovers a concurrent create. No failed response is retried
	// blindly; callers reconcile this same deterministic allocation on retry.
	exists, err = p.inspect(ctx, name, config)
	if err == nil && !exists {
		err = ErrRuntimePreparationUnavailable
	}
	return name, err
}

func (p *RuntimePreparer) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, runtimeEnginePrefix+path, bytes.NewReader(body))
	if err != nil {
		return nil, ErrRuntimePreparationUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return nil, ErrRuntimePreparationUnavailable
	}
	return response, nil
}

func (p *RuntimePreparer) inspect(ctx context.Context, name string, expected stoppedRuntimeConfig) (bool, error) {
	response, err := p.request(ctx, http.MethodGet, name+"/json", nil)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, ErrRuntimePreparationUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return false, ErrRuntimePreparationUnavailable
	}
	var actual struct {
		Name       string
		Config     stoppedRuntimeConfig
		HostConfig stoppedRuntimeHost
		State      struct {
			Status     string
			Running    bool
			Restarting bool
			Paused     bool
		}
		Mounts []json.RawMessage
	}
	if json.Unmarshal(data, &actual) != nil {
		return false, ErrRuntimePreparationUnavailable
	}
	actual.Config.HostConfig = actual.HostConfig
	// Docker normalizes empty arrays to null. Treat those representations alike.
	normalize := func(c *stoppedRuntimeConfig) {
		if len(c.Cmd) == 0 {
			c.Cmd = nil
		}
		if len(c.Env) == 0 {
			c.Env = nil
		}
		if len(c.HostConfig.CapAdd) == 0 {
			c.HostConfig.CapAdd = nil
		}
		if len(c.HostConfig.Binds) == 0 {
			c.HostConfig.Binds = nil
		}
		if len(c.HostConfig.Mounts) == 0 {
			c.HostConfig.Mounts = nil
		}
		if len(c.HostConfig.Devices) == 0 {
			c.HostConfig.Devices = nil
		}
		if len(c.HostConfig.DeviceRequests) == 0 {
			c.HostConfig.DeviceRequests = nil
		}
		if len(c.HostConfig.PortBindings) == 0 {
			c.HostConfig.PortBindings = nil
		}
		if len(c.HostConfig.VolumesFrom) == 0 {
			c.HostConfig.VolumesFrom = nil
		}
		// Docker supplies its private IPC default in inspect.
		if c.HostConfig.IpcMode == "private" {
			c.HostConfig.IpcMode = ""
		}
	}
	normalize(&actual.Config)
	normalize(&expected)
	if actual.Name != "/"+name || actual.State.Status != "created" || actual.State.Running || actual.State.Restarting || actual.State.Paused ||
		len(actual.Mounts) != 0 || !reflect.DeepEqual(actual.Config, expected) {
		return true, ErrRuntimePreparationDrift
	}
	return true, nil
}
