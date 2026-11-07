// Package daemon loads immutable per-process policy and composes scoped worker
// and scheduler lifecycles. It never loads staff/replay assertion authority.
package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"hello/internal/deployment"
	"hello/internal/dispatch"
	"hello/internal/jsonobject"
	"hello/internal/kafka"
	"hello/internal/observability"
	"hello/internal/runloop"
	"hello/internal/scheduler"
)

// ErrConfiguration contains no credential, path or decoder details.
var ErrConfiguration = deployment.ErrConfiguration

// Configuration is loaded once and retained privately for one process role.
type Configuration struct {
	monitoring *observability.Admin
	role       deployment.Component
	database   *pgxpool.Config
	source     dispatch.SourceManifest
	broker     kafka.ScopedProducerConfig
	admission  dispatch.KafkaAdmissionConfig
	scheduler  scheduler.Config
	retry      runloop.RetryPolicy
	delivery   *deliveryConfig
}

// Load validates a caller-owned trusted policy stream without network I/O.
// Each role has an exact schema; operator JWT/key/grant settings are rejected.
func Load(role deployment.Component, reader io.Reader) (*Configuration, error) {
	if !daemonRole(role) || !deployment.EnvironmentAllowed() {
		return nil, ErrConfiguration
	}
	data, err := jsonobject.Read(reader)
	if err != nil {
		return nil, ErrConfiguration
	}
	cfg := &Configuration{role: role}
	var databaseURL, sourceID, groupID, mode string
	var brokerPolicy, deliveryPolicy, admin json.RawMessage
	var retryMin, retryMax, poll int64
	var page int32
	fields := map[string]any{
		"database_url": &databaseURL, "log_source_id": &sourceID, "cluster_identity": &cfg.source.ClusterID,
		"topic_name": &cfg.source.Topic, "topic_incarnation": &cfg.source.Incarnation, "partition_count": &cfg.source.PartitionCount,
		"kafka": &brokerPolicy, "retry_min_ms": &retryMin, "retry_max_ms": &retryMax,
	}
	if role == deployment.Worker {
		// Select the schema only. Decode below still rejects duplicate mode
		// fields, case aliases, unknown members and missing policy.
		var selector struct {
			Mode string `json:"mode"`
		}
		if json.Unmarshal(data, &selector) != nil {
			return nil, ErrConfiguration
		}
		switch selector.Mode {
		case "admission_only":
			fields["group_id"] = &groupID
		case "delivery_only":
			fields["delivery"] = &deliveryPolicy
		default:
			return nil, ErrConfiguration
		}
		fields["mode"] = &mode
	} else {
		fields["poll_interval_ms"] = &poll
		fields["page_size"] = &page
	}
	if jsonobject.DecodeOptional(data, fields, map[string]any{"monitoring": &admin}) != nil || retryMin < 100 || retryMax < retryMin || retryMax > 300000 {
		return nil, ErrConfiguration
	}
	cfg.monitoring, err = observability.ParseAdmin(admin)
	if err != nil {
		return nil, ErrConfiguration
	}
	cfg.retry = runloop.RetryPolicy{MinimumDelay: time.Duration(retryMin) * time.Millisecond, MaximumDelay: time.Duration(retryMax) * time.Millisecond}
	if len(sourceID) != 36 || cfg.source.ID.Scan(sourceID) != nil || cfg.source.ID.String() != sourceID || cfg.source.Validate() != nil {
		return nil, ErrConfiguration
	}
	cfg.broker, err = deployment.Broker(brokerPolicy, cfg.source.Topic)
	if err != nil {
		return nil, ErrConfiguration
	}
	switch {
	case mode == "admission_only":
		cfg.admission = dispatch.KafkaAdmissionConfig{Brokers: cfg.broker.Brokers, GroupID: groupID, SourceID: cfg.source.ID, ClusterID: cfg.source.ClusterID, Topic: cfg.source.Topic, Incarnation: cfg.source.Incarnation, PartitionCount: cfg.source.PartitionCount, TLS: cfg.broker.TLS, SASL: cfg.broker.SASL}
		if cfg.admission.Validate() != nil {
			return nil, ErrConfiguration
		}
	case mode == "delivery_only":
		cfg.delivery, err = parseDelivery(deliveryPolicy)
		if err != nil {
			return nil, ErrConfiguration
		}
	default:
		if poll < 100 || poll > 60000 || page < 1 || page > 100 {
			return nil, ErrConfiguration
		}
		cfg.scheduler = scheduler.Config{Interval: time.Duration(poll) * time.Millisecond, PageSize: page, Retry: cfg.retry}
	}
	cfg.database, err = deployment.Database(databaseURL, "hello-"+string(role))
	if err != nil {
		return nil, ErrConfiguration
	}
	return cfg, nil
}

// LoadDeployment reads the fixed protected policy for this compiled daemon role.
// It does not accept environment, argv or replay credential configuration.
func LoadDeployment(role deployment.Component) (*Configuration, error) {
	if !daemonRole(role) {
		return nil, ErrConfiguration
	}
	data, err := deployment.Read(role)
	if err != nil {
		return nil, ErrConfiguration
	}
	cfg, err := Load(role, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if deployment.CheckDatabaseFiles(cfg.database.ConnString()) != nil {
		return nil, ErrConfiguration
	}
	return cfg, nil
}
func daemonRole(role deployment.Component) bool {
	return role == deployment.Worker || role == deployment.Scheduler
}
