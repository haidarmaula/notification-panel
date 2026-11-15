package onboarding

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"hello/internal/jsonobject"
)

const runtimePolicyLimit = 64 * 1024

var (
	ErrInvalidRuntimePolicy        = errors.New("invalid onboarding runtime policy")
	ErrRuntimeCapacityUnconfigured = errors.New("onboarding runtime capacity is not configured")
	ErrRuntimeCapacityExceeded     = errors.New("onboarding runtime capacity exceeded")
	ErrInvalidRuntimePlan          = errors.New("invalid onboarding runtime plan")
)

var (
	runtimeImagePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]{0,254}@sha256:([0-9a-f]{64})$`)
	runtimeNetworkPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,63}$`)
	runtimeImageSegment   = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)
)

const (
	runtimeDeliveryMinMemory int64 = 64 << 20
	runtimeDeliveryMaxMemory int64 = 4 << 30
)

// RuntimePolicy contains the protected immutable subset used to plan one delivery runtime.
// It does not represent the complete platform profile described by the design.
type RuntimePolicy struct {
	environment            Environment
	revision               int64
	deliveryImage          string
	network                string
	maxApplications        int32
	maxOverlap             int32
	maxDatabaseConnections int32
	deliveryMemoryBytes    int64
	deliveryNanoCPUs       int64
	deliveryPIDsLimit      int32
	deliveryDatabaseConns  int32
}

// RuntimeDeliveryPlan is a scoped immutable description, not Docker input.
// Its fields are returned by value from RuntimePlan.Delivery.
type RuntimeDeliveryPlan struct {
	ApplicationID            string
	ConfigurationID          string
	Generation               int64
	Environment              Environment
	PolicyRevision           int64
	ImageDigest              string
	Network                  string
	MemoryBytes              int64
	NanoCPUs                 int64
	PIDsLimit                int32
	DatabaseConnections      int32
	Replicas                 int32
	Concurrency              int32
	RequestsPerSecond        int32
	ProjectRequestsPerSecond int32
	UID                      int32
	GID                      int32
	ReadOnlyRoot             bool
	DropAllCapabilities      bool
	NoNewPrivileges          bool
}

// RuntimeAcknowledgement identifies shared components that must independently
// report the exact application generation before a runtime can be considered ready.
type RuntimeAcknowledgement struct {
	Component       string
	ApplicationID   string
	ConfigurationID string
	Generation      int64
	PolicyRevision  int64
}

// RuntimePlan contains the one per-application delivery plan and required
// shared-component acknowledgements. It does not assert that any component is ready.
type RuntimePlan struct {
	delivery RuntimeDeliveryPlan
	shared   []RuntimeAcknowledgement
}

type runtimePolicyWire struct {
	Version                int32           `json:"version"`
	Environment            string          `json:"environment"`
	Revision               int64           `json:"revision"`
	DeliveryImage          string          `json:"delivery_image"`
	Network                string          `json:"network"`
	MaxApplications        int32           `json:"max_applications"`
	MaxOverlap             int32           `json:"max_overlap"`
	MaxDatabaseConnections int32           `json:"max_database_connections"`
	Delivery               json.RawMessage `json:"delivery"`
}

type runtimeDeliveryWire struct {
	MemoryBytes            int64
	NanoCPUs               int64
	PIDsLimit              int32
	MaxDatabaseConnections int32
}

// ParseRuntimePolicy parses the explicitly approved bounded v1 runtime subset.
func ParseRuntimePolicy(data []byte) (*RuntimePolicy, error) {
	if len(data) == 0 || len(data) > runtimePolicyLimit || !utf8.Valid(data) || !jsonobject.UniqueLowercaseFields(data) {
		return nil, ErrInvalidRuntimePolicy
	}
	var wire runtimePolicyWire
	if jsonobject.Decode(data, map[string]any{
		"version": &wire.Version, "environment": &wire.Environment, "revision": &wire.Revision,
		"delivery_image": &wire.DeliveryImage, "network": &wire.Network,
		"max_applications": &wire.MaxApplications, "max_overlap": &wire.MaxOverlap,
		"max_database_connections": &wire.MaxDatabaseConnections, "delivery": &wire.Delivery,
	}) != nil {
		return nil, ErrInvalidRuntimePolicy
	}
	var delivery runtimeDeliveryWire
	if jsonobject.Decode(wire.Delivery, map[string]any{
		"memory_bytes": &delivery.MemoryBytes, "nano_cpus": &delivery.NanoCPUs,
		"pids_limit": &delivery.PIDsLimit, "max_database_connections": &delivery.MaxDatabaseConnections,
	}) != nil || wire.Version != 1 || wire.Revision <= 0 ||
		(wire.Environment != string(EnvironmentStaging) && wire.Environment != string(EnvironmentProduction)) ||
		!validRuntimeImage(wire.DeliveryImage) || !validRuntimeNetwork(wire.Network) ||
		wire.MaxApplications < 1 || wire.MaxApplications > 100 || wire.MaxOverlap < 1 || wire.MaxOverlap > wire.MaxApplications ||
		wire.MaxDatabaseConnections < 1 || wire.MaxDatabaseConnections > 10000 ||
		delivery.MemoryBytes < runtimeDeliveryMinMemory || delivery.MemoryBytes > runtimeDeliveryMaxMemory ||
		delivery.NanoCPUs < 100_000_000 || delivery.NanoCPUs > 4_000_000_000 ||
		delivery.PIDsLimit < 16 || delivery.PIDsLimit > 512 ||
		delivery.MaxDatabaseConnections < 2 || delivery.MaxDatabaseConnections > 10 {
		return nil, ErrInvalidRuntimePolicy
	}
	poolBudget := int64(wire.MaxApplications+wire.MaxOverlap) * int64(delivery.MaxDatabaseConnections)
	if poolBudget > int64(wire.MaxDatabaseConnections) {
		return nil, ErrInvalidRuntimePolicy
	}
	return &RuntimePolicy{
		environment: Environment(wire.Environment), revision: wire.Revision, deliveryImage: wire.DeliveryImage,
		network: wire.Network, maxApplications: wire.MaxApplications, maxOverlap: wire.MaxOverlap,
		maxDatabaseConnections: wire.MaxDatabaseConnections, deliveryMemoryBytes: delivery.MemoryBytes,
		deliveryNanoCPUs: delivery.NanoCPUs, deliveryPIDsLimit: delivery.PIDsLimit,
		deliveryDatabaseConns: delivery.MaxDatabaseConnections,
	}, nil
}

// ValidateCapacity checks configured application and replacement budgets.
func (p *RuntimePolicy) ValidateCapacity(applications, overlappingReplacements int32) error {
	if p == nil || p.maxApplications <= 0 || p.maxDatabaseConnections <= 0 || p.deliveryDatabaseConns <= 0 {
		return ErrRuntimeCapacityUnconfigured
	}
	if applications < 0 || overlappingReplacements < 0 || applications > p.maxApplications ||
		overlappingReplacements > p.maxOverlap ||
		int64(applications+overlappingReplacements)*int64(p.deliveryDatabaseConns) > int64(p.maxDatabaseConnections) {
		return ErrRuntimeCapacityExceeded
	}
	return nil
}

// BuildRuntimePlan scopes a deterministic delivery plan to canonical IDs and a positive generation.
func (p *RuntimePolicy) BuildRuntimePlan(applicationID, configurationID string, environment Environment, generation int64) (RuntimePlan, error) {
	var plan RuntimePlan
	if p == nil || p.revision <= 0 || !validRuntimeUUID(applicationID) || !validRuntimeUUID(configurationID) ||
		environment != p.environment || generation <= 0 {
		return plan, ErrInvalidRuntimePlan
	}
	plan.delivery = RuntimeDeliveryPlan{
		ApplicationID: applicationID, ConfigurationID: configurationID, Generation: generation,
		Environment: p.environment, PolicyRevision: p.revision, ImageDigest: p.deliveryImage,
		Network: p.network, MemoryBytes: p.deliveryMemoryBytes, NanoCPUs: p.deliveryNanoCPUs,
		PIDsLimit: p.deliveryPIDsLimit, DatabaseConnections: p.deliveryDatabaseConns,
		Replicas: 1, Concurrency: 1, RequestsPerSecond: 1, ProjectRequestsPerSecond: 1, UID: 65532, GID: 65532,
		ReadOnlyRoot: true, DropAllCapabilities: true, NoNewPrivileges: true,
	}
	for _, component := range []string{"SERVER", "ADMISSION", "SCHEDULER"} {
		plan.shared = append(plan.shared, RuntimeAcknowledgement{
			Component: component, ApplicationID: applicationID, ConfigurationID: configurationID,
			Generation: generation, PolicyRevision: p.revision,
		})
	}
	return plan, nil
}

// Delivery returns a copy of the app-scoped delivery plan.
func (p RuntimePlan) Delivery() RuntimeDeliveryPlan { return p.delivery }

// RequiredSharedAcknowledgements returns a copy of the required shared runtime acknowledgements.
func (p RuntimePlan) RequiredSharedAcknowledgements() []RuntimeAcknowledgement {
	return append([]RuntimeAcknowledgement(nil), p.shared...)
}

func validRuntimeNetwork(value string) bool {
	return runtimeNetworkPattern.MatchString(value) && !strings.HasPrefix(value, "-") && value != "." && value != ".."
}

func validRuntimeImage(value string) bool {
	matches := runtimeImagePattern.FindStringSubmatch(value)
	if len(matches) != 2 || len(value) > 326 {
		return false
	}
	repository := strings.TrimSuffix(value, "@sha256:"+matches[1])
	if len(repository) == 0 || len(repository) > 255 || strings.Contains(repository, "://") || strings.Contains(repository, "//") {
		return false
	}
	parts := strings.Split(repository, "/")
	if len(parts) == 1 && strings.Contains(parts[0], ":") {
		return false
	}
	for i, part := range parts {
		if part == "" {
			return false
		}
		if i == 0 && strings.Contains(part, ":") {
			host, port, found := strings.Cut(part, ":")
			if !found || strings.Contains(port, ":") || !validRuntimeRegistryHost(host) || !validRuntimePort(port) {
				return false
			}
			continue
		}
		if !runtimeImageSegment.MatchString(part) {
			return false
		}
	}
	return true
}

func validRuntimeRegistryHost(host string) bool {
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return false
			}
		}
	}
	return true
}

func validRuntimePort(value string) bool {
	port, err := strconv.ParseUint(value, 10, 16)
	return err == nil && port > 0
}

func validRuntimeUUID(value string) bool {
	return validUUID(value)
}
