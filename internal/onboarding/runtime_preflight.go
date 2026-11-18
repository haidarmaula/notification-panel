package onboarding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"hello/internal/deployment"
	"hello/internal/provisioning"
)

const (
	preflightDBCAHost   = "/etc/hello/runtime-preparation-postgres-ca.crt"
	preflightDBCATarget = "/etc/hello/postgres-ca.crt"
)

var dockerContainerID = regexp.MustCompile("^[0-9a-f]{64}$")

type runtimeBindMount struct {
	Type     string
	Source   string
	Target   string
	ReadOnly bool
}

type runtimeInspection struct {
	ID         string `json:"Id"`
	Name       string
	Config     stoppedRuntimeConfig
	HostConfig stoppedRuntimeHost
	State      struct {
		Status     string
		Running    bool
		Restarting bool
		Paused     bool
		ExitCode   int
		OOMKilled  bool
		Error      string
	}
	Mounts []struct {
		Type        string
		Source      string
		Destination string
		RW          bool
	}
}

func (p *RuntimePreparer) inspectRuntime(ctx context.Context, id string) (*runtimeInspection, error) {
	response, err := p.request(ctx, http.MethodGet, id+"/json", nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, ErrRuntimePreparationUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	var actual runtimeInspection
	if err != nil || len(data) > 64*1024 || json.Unmarshal(data, &actual) != nil {
		return nil, ErrRuntimePreparationUnavailable
	}
	actual.Config.HostConfig = actual.HostConfig
	return &actual, nil
}

// DeliveryPreflightEvidence binds the worker policy to the reserved provider
// configuration and to an independently approved runtime LOGIN.
type DeliveryPreflightEvidence struct {
	ProjectID           string
	ServiceAccountEmail string
	DatabaseUsername    string
	PolicySHA256        [32]byte
	NeedsDBCA           bool
}

// CheckDeliveryPreflight rechecks the reservation and exact owner-approved worker policy.
func (r *RuntimeReservation) CheckDeliveryPreflight(ctx context.Context, plan RuntimeDeliveryPlan, evidence DeliveryPreflightEvidence) error {
	if r == nil || r.queries == nil || plan != r.plan || evidence.PolicySHA256 == [32]byte{} ||
		evidence.ProjectID == "" || evidence.ServiceAccountEmail == "" || evidence.DatabaseUsername == "" {
		return ErrRuntimeReservationUnavailable
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var allowed bool
	err := r.queries.QueryRow(callCtx, `SELECT public.check_application_onboarding_worker_preflight($1,$2,$3,$4,$5,$6,$7)`,
		runtimeUUID(plan.ApplicationID), runtimeUUID(r.request.ID), r.request.PolicySHA256[:],
		evidence.ProjectID, evidence.ServiceAccountEmail, evidence.PolicySHA256[:], evidence.DatabaseUsername).Scan(&allowed)
	if err != nil || !allowed {
		return ErrRuntimeReservationUnavailable
	}
	return nil
}

type deliveryPreflightGuard interface {
	CheckDeliveryPreflight(context.Context, RuntimeDeliveryPlan, DeliveryPreflightEvidence) error
}

// RunDeliveryPreflight runs only the worker's fixed one-shot check. The
// existing stopped allocation name is reused so the same generation cannot
// consume a second slot. Its result cannot acknowledge a live runtime.
func (p *RuntimePreparer) RunDeliveryPreflight(ctx context.Context, plan RuntimePlan, evidence DeliveryPreflightEvidence) (string, error) {
	if p == nil || p.client == nil {
		return "", ErrRuntimePreparationUnavailable
	}
	guard, ok := p.guard.(deliveryPreflightGuard)
	if !ok {
		return "", ErrRuntimeReservationUnavailable
	}
	d := plan.Delivery()
	check := func() error { return guard.CheckDeliveryPreflight(ctx, d, evidence) }
	if err := check(); err != nil {
		return "", err
	}
	config, name, err := deliveryPreflightEnvelope(d, evidence)
	if err != nil {
		return "", err
	}
	// The immutable generation directory provides a stable host-local lock
	// across CLI invocations. Docker Start can restart an exited container.
	dir, err := os.Open(filepath.Dir(config.HostConfig.Mounts[0].Source))
	if err != nil {
		return name, ErrRuntimePreparationUnavailable
	}
	defer dir.Close()
	if syscall.Flock(int(dir.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return name, ErrRuntimePreparationUnavailable
	}
	if err := p.replaceStoppedAllocation(ctx, check, stoppedRuntimeEnvelope(d), name); err != nil {
		return name, err
	}
	return p.runDeliveryPreflight(ctx, check, d.ApplicationID, evidence.PolicySHA256, config, name)
}

func (p *RuntimePreparer) replaceStoppedAllocation(ctx context.Context, check func() error, stopped stoppedRuntimeConfig, name string) error {
	actual, err := p.inspectRuntime(ctx, name)
	if err != nil || !validStoppedRuntimeInspection(actual, name, stopped) {
		return err
	}
	if !dockerContainerID.MatchString(actual.ID) {
		return ErrRuntimePreparationDrift
	}
	if err := check(); err != nil {
		return err
	}
	// Delete only the inert envelope by immutable ID, without force or volume removal.
	response, err := p.request(ctx, http.MethodDelete, actual.ID+"?force=false&v=false", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		return ErrRuntimePreparationUnavailable
	}
	return nil
}

func (p *RuntimePreparer) runDeliveryPreflight(ctx context.Context, check func() error, applicationID string, digest [32]byte, config stoppedRuntimeConfig, name string) (string, error) {
	actual, err := p.inspectRuntime(ctx, name)
	if err != nil {
		return name, err
	}
	if actual == nil {
		if err := check(); err != nil {
			return name, err
		}
		encoded, _ := json.Marshal(config)
		status, err := p.preflightEffect(ctx, "create?name="+name, encoded)
		if err != nil || (status != http.StatusCreated && status != http.StatusConflict) {
			return name, ErrRuntimePreparationUnavailable
		}
		actual, err = p.inspectRuntime(ctx, name)
		if err != nil {
			return name, err
		}
	}
	if !validPreflightInspection(actual, name, config) {
		return name, ErrRuntimePreparationDrift
	}
	id := actual.ID
	if actual.State.Status == "created" {
		if err := check(); err != nil {
			return name, err
		}
		status, err := p.preflightEffect(ctx, id+"/start", nil)
		if err != nil || (status != http.StatusNoContent && status != http.StatusNotModified) {
			return name, ErrRuntimePreparationUnavailable
		}
	}

	// Inspect by immutable ID after start. An ambiguous request is reconciled
	// on the next invocation without starting an exited check again.
	for {
		actual, err = p.inspectRuntime(ctx, id)
		if err != nil {
			return name, err
		}
		if !validPreflightInspection(actual, name, config) || actual.ID != id {
			return name, ErrRuntimePreparationDrift
		}
		switch actual.State.Status {
		case "exited":
			if actual.State.ExitCode != 0 || actual.State.OOMKilled || actual.State.Error != "" || !p.verifyDeliveryPreflightOutput(ctx, id, applicationID, digest) {
				return name, ErrRuntimePreparationUnavailable
			}
			return name, check()
		case "running":
		default:
			return name, ErrRuntimePreparationDrift
		}
		select {
		case <-ctx.Done():
			return name, ErrRuntimePreparationUnavailable
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func deliveryPreflightEnvelope(d RuntimeDeliveryPlan, evidence DeliveryPreflightEvidence) (stoppedRuntimeConfig, string, error) {
	switch d.Network {
	case "", "host", "default", "bridge", "none":
		return stoppedRuntimeConfig{}, "", ErrInvalidRuntimePlan
	}
	workerPolicy, err := provisioning.DeliveryPolicyPath(d.ApplicationID, string(d.Environment), d.ConfigurationID, d.Generation)
	if err != nil {
		return stoppedRuntimeConfig{}, "", ErrInvalidRuntimePlan
	}
	contents, err := deployment.ReadSecret(workerPolicy)
	if err != nil || sha256.Sum256(contents) != evidence.PolicySHA256 {
		return stoppedRuntimeConfig{}, "", ErrRuntimePreparationUnavailable
	}
	const credentialName = "service-account.json"
	credential := filepath.Join("/var/lib/hello/credentials", d.ApplicationID, string(d.Environment), d.ConfigurationID, credentialName)
	if _, err := deployment.ReadSecret(credential); err != nil {
		return stoppedRuntimeConfig{}, "", ErrRuntimePreparationUnavailable
	}
	config := stoppedRuntimeEnvelope(d)
	name := stoppedRuntimeName(config)
	config.Cmd = []string{"--check-delivery-policy"}
	config.Labels["hello.onboarding.state"] = "delivery-preflight"
	config.Labels["hello.onboarding.policy-sha256"] = hex.EncodeToString(evidence.PolicySHA256[:])
	config.HostConfig.NetworkMode = d.Network
	config.HostConfig.LogConfig = &runtimeLogConfig{Type: "json-file", Config: map[string]string{"max-size": "1m", "max-file": "1"}}
	config.HostConfig.Mounts = []runtimeBindMount{
		{Type: "bind", Source: workerPolicy, Target: "/etc/hello/worker.json", ReadOnly: true},
		{Type: "bind", Source: credential, Target: "/etc/hello/fcm/" + credentialName, ReadOnly: true},
	}
	if evidence.NeedsDBCA {
		// The DB CA contains no private key. The worker rechecks in-container ownership and TLS.
		if err := deployment.CheckDatabaseFiles("postgres://db/hello?sslrootcert=" + preflightDBCAHost); err != nil {
			return stoppedRuntimeConfig{}, "", ErrRuntimePreparationUnavailable
		}
		config.HostConfig.Mounts = append(config.HostConfig.Mounts,
			runtimeBindMount{Type: "bind", Source: preflightDBCAHost, Target: preflightDBCATarget, ReadOnly: true})
	}
	return config, name, nil
}

func validPreflightInspection(actual *runtimeInspection, name string, config stoppedRuntimeConfig) bool {
	if actual == nil || !dockerContainerID.MatchString(actual.ID) || actual.Name != "/"+name ||
		actual.State.Restarting || actual.State.Paused || actual.State.Running != (actual.State.Status == "running") ||
		!sameRuntimeConfig(actual.Config, config) || len(actual.Mounts) != len(config.HostConfig.Mounts) {
		return false
	}
	for _, expected := range config.HostConfig.Mounts {
		found := false
		for _, actual := range actual.Mounts {
			if actual.Type == "bind" && actual.Source == expected.Source && actual.Destination == expected.Target && !actual.RW {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (p *RuntimePreparer) preflightEffect(ctx context.Context, path string, body []byte) (int, error) {
	response, err := p.request(ctx, http.MethodPost, path, body)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

// Verify the worker's own report, which hashes the bytes read inside Docker.
// With TTY disabled the Engine multiplexes each log record with an 8-byte header.
func (p *RuntimePreparer) verifyDeliveryPreflightOutput(ctx context.Context, id, applicationID string, digest [32]byte) bool {
	response, err := p.request(ctx, http.MethodGet, id+"/logs?stdout=1&tail=2", nil)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	const maxOutput = 1024
	frames, err := io.ReadAll(io.LimitReader(response.Body, maxOutput+1))
	if response.StatusCode != http.StatusOK || err != nil || len(frames) > maxOutput {
		return false
	}
	var stdout bytes.Buffer
	for len(frames) > 0 {
		if len(frames) < 8 || frames[0] != 1 || frames[1] != 0 || frames[2] != 0 || frames[3] != 0 {
			return false
		}
		size := uint64(binary.BigEndian.Uint32(frames[4:8]))
		if size == 0 || size > uint64(len(frames)-8) {
			return false
		}
		end := 8 + int(size)
		stdout.Write(frames[8:end])
		frames = frames[end:]
	}
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	// A host failure can leave an uncertain Start request. Every retained
	// stdout record must describe the exact same approved scope and bytes.
	for records := 0; ; records++ {
		var reported struct {
			ApplicationID string `json:"application_id"`
			PolicySHA256  string `json:"policy_sha256"`
		}
		err := decoder.Decode(&reported)
		if err == io.EOF {
			return records > 0
		}
		if err != nil || reported.ApplicationID != applicationID || reported.PolicySHA256 != hex.EncodeToString(digest[:]) {
			return false
		}
	}
}
