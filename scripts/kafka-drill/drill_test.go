//go:build integration

package kafkadrill

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	broker "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/protocol"
	"github.com/segmentio/kafka-go/protocol/metadata"
	"github.com/stretchr/testify/require"
	"hello/internal/kafka"
)

const send = "notification.send.requested"
const updates = "notification.delivery.updated"

//go:embed profile.json
var policy []byte

type profile struct {
	Environment string
	ClusterID   string `json:"cluster_id"`
	Topics      []string
	Config      map[string]string
	Replicas    [][]int
	ACLs        [][4]string
}

type drill struct {
	t     *testing.T
	ctx   context.Context
	p     profile
	admin *broker.Client
}

func (d drill) client(role string) *broker.Client {
	d.t.Helper()
	dir := "/fixture/"
	identity, err := tls.LoadX509KeyPair(dir+role+".crt", dir+role+".key")
	require.NoError(d.t, err)
	ca, err := os.ReadFile(dir + "ca.crt")
	require.NoError(d.t, err)
	roots := x509.NewCertPool()
	require.True(d.t, roots.AppendCertsFromPEM(ca))
	tr := &broker.Transport{TLS: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{identity}}, DialTimeout: time.Second}
	d.t.Cleanup(tr.CloseIdleConnections)
	return &broker.Client{Addr: broker.TCP("b3:9092", "b2:9092", "b1:9092"), Timeout: 3 * time.Second, Transport: tr}
}

func (d drill) metadata(topics ...string) *broker.MetadataResponse {
	d.t.Helper()
	m, err := d.admin.Metadata(d.ctx, &broker.MetadataRequest{Topics: topics})
	require.NoError(d.t, err)
	require.Equal(d.t, "isolated-kafka-drill", d.p.Environment)
	require.Equal(d.t, d.p.ClusterID, m.ClusterID)
	return m
}

func (d drill) provision(mode string) {
	d.t.Helper()
	m := d.metadata(d.p.Topics...)
	racks := map[string]bool{}
	for _, b := range m.Brokers {
		racks[b.Rack] = true
	}
	require.Len(d.t, m.Brokers, 3)
	require.Equal(d.t, map[string]bool{"rack-1": true, "rack-2": true, "rack-3": true}, racks)
	require.Len(d.t, m.Topics, len(d.p.Topics))
	var create []broker.TopicConfig
	for _, topic := range m.Topics {
		require.Contains(d.t, d.p.Topics, topic.Name)
		if topic.Error == broker.UnknownTopicOrPartition {
			cfg := broker.TopicConfig{Topic: topic.Name, NumPartitions: -1, ReplicationFactor: -1}
			for k, v := range d.p.Config {
				cfg.ConfigEntries = append(cfg.ConfigEntries, broker.ConfigEntry{ConfigName: k, ConfigValue: v})
			}
			for i, replicas := range d.p.Replicas {
				cfg.ReplicaAssignments = append(cfg.ReplicaAssignments, broker.ReplicaAssignment{Partition: i, Replicas: replicas})
			}
			create = append(create, cfg)
			continue
		}
		require.NoError(d.t, topic.Error)
		require.Len(d.t, topic.Partitions, len(d.p.Replicas))
		for _, p := range topic.Partitions {
			require.NoError(d.t, p.Error)
			require.GreaterOrEqual(d.t, p.ID, 0)
			require.Less(d.t, p.ID, len(d.p.Replicas))
			var replicas []int
			for _, r := range p.Replicas {
				replicas = append(replicas, r.ID)
			}
			require.Equal(d.t, d.p.Replicas[p.ID], replicas)
		}
		cfg, err := d.admin.DescribeConfigs(d.ctx, &broker.DescribeConfigsRequest{Resources: []broker.DescribeConfigRequestResource{{ResourceType: broker.ResourceTypeTopic, ResourceName: topic.Name}}})
		require.NoError(d.t, err)
		require.Len(d.t, cfg.Resources, 1)
		require.NoError(d.t, cfg.Resources[0].Error)
		got := map[string]string{}
		for _, c := range cfg.Resources[0].ConfigEntries {
			if _, ok := d.p.Config[c.ConfigName]; ok {
				got[c.ConfigName] = c.ConfigValue
			}
		}
		require.Equal(d.t, d.p.Config, got, "topic config drift on %s", topic.Name)
	}
	wanted := map[broker.ACLEntry]bool{}
	for _, row := range d.p.ACLs {
		var resource broker.ResourceType
		var operation broker.ACLOperationType
		require.NoError(d.t, resource.UnmarshalText([]byte(row[1])))
		require.NoError(d.t, operation.UnmarshalText([]byte(row[3])))
		wanted[broker.ACLEntry{ResourceType: resource, ResourceName: row[2], ResourcePatternType: broker.PatternTypeLiteral, Principal: "User:CN=fixture-" + row[0], Host: "*", Operation: operation, PermissionType: broker.ACLPermissionTypeAllow}] = true
	}
	acls, err := d.admin.DescribeACLs(d.ctx, &broker.DescribeACLsRequest{Filter: broker.ACLFilter{ResourceTypeFilter: broker.ResourceTypeAny, ResourcePatternTypeFilter: broker.PatternTypeAny, Operation: broker.ACLOperationTypeAny, PermissionType: broker.ACLPermissionTypeAny}})
	require.NoError(d.t, err)
	require.NoError(d.t, acls.Error)
	for _, resource := range acls.Resources {
		for _, a := range resource.ACLs {
			entry := broker.ACLEntry{ResourceType: resource.ResourceType, ResourceName: resource.ResourceName, ResourcePatternType: resource.PatternType, Principal: a.Principal, Host: a.Host, Operation: a.Operation, PermissionType: a.PermissionType}
			require.Contains(d.t, wanted, entry, "unexpected ACL; review before applying")
			delete(wanted, entry)
		}
	}
	d.t.Logf("profile=%s missing topics=%d missing ACLs=%d", d.p.Environment, len(create), len(wanted))
	if mode == "plan" {
		return
	}
	if mode == "check" {
		require.Empty(d.t, create)
		require.Empty(d.t, wanted)
		return
	}
	if len(create) > 0 {
		r, err := d.admin.CreateTopics(d.ctx, &broker.CreateTopicsRequest{Topics: create})
		require.NoError(d.t, err)
		require.Len(d.t, r.Errors, len(create))
		for _, err := range r.Errors {
			require.NoError(d.t, err)
		}
	}
	var add []broker.ACLEntry
	for a := range wanted {
		add = append(add, a)
	}
	if len(add) > 0 {
		r, err := d.admin.CreateACLs(d.ctx, &broker.CreateACLsRequest{ACLs: add})
		require.NoError(d.t, err)
		require.Len(d.t, r.Errors, len(add))
		for _, err := range r.Errors {
			require.NoError(d.t, err)
		}
	}
}

func (d drill) layout(n int, initial bool) {
	d.t.Helper()
	topics := append(slices.Clone(d.p.Topics), "__consumer_offsets")
	m := d.metadata(topics...)
	require.Len(d.t, m.Topics, len(topics))
	for _, topic := range m.Topics {
		require.NoError(d.t, topic.Error)
		require.Len(d.t, topic.Partitions, 3)
		for _, p := range topic.Partitions {
			require.NoError(d.t, p.Error)
			require.Len(d.t, p.Replicas, 3)
			require.Len(d.t, p.Isr, n, "topic %s partition %d", topic.Name, p.ID)
			if p.ID == 2 && topic.Name != "__consumer_offsets" {
				d.t.Logf("topic=%s partition=2 leader=%d ISR=%d", topic.Name, p.Leader.ID, len(p.Isr))
				if initial {
					require.Equal(d.t, 3, p.Leader.ID)
				}
				if n == 2 {
					require.NotEqual(d.t, 3, p.Leader.ID)
				}
				if n == 1 {
					require.Equal(d.t, 1, p.Leader.ID)
				}
			}
		}
	}
}

func (d drill) produce(c *broker.Client, topic, body string) (int64, error) {
	r, err := c.Produce(d.ctx, &broker.ProduceRequest{Topic: topic, Partition: 2, RequiredAcks: broker.RequireAll,
		Records: broker.NewRecordReader(broker.Record{Key: broker.NewBytes([]byte("fixture-canary")), Value: broker.NewBytes([]byte(body))})})
	if err != nil {
		return -1, err
	}
	return r.BaseOffset, r.Error
}

func (d drill) read(c *broker.Client, topic string, partition int, offset int64) (string, error) {
	r, err := c.Fetch(d.ctx, &broker.FetchRequest{Topic: topic, Partition: partition, Offset: offset, MinBytes: 1, MaxBytes: 65536, MaxWait: 100 * time.Millisecond})
	if err != nil {
		return "", err
	}
	if r.Error != nil {
		return "", r.Error
	}
	for {
		rec, err := r.Records.ReadRecord()
		if err != nil {
			return "", err
		}
		if rec.Key != nil {
			require.NoError(d.t, rec.Key.Close())
		}
		body, err := broker.ReadAll(rec.Value)
		if rec.Value != nil {
			require.NoError(d.t, rec.Value.Close())
		}
		if err != nil {
			return "", err
		}
		if rec.Offset < offset {
			continue
		}
		require.Equal(d.t, offset, rec.Offset)
		return string(body), nil
	}
}

func (d drill) deniedTopic(c *broker.Client, topic string) {
	d.t.Helper()
	// The transport's all-topics cache maps invisible topics to UnknownTopic.
	// Ask the broker about this specific topic to verify the authorization code.
	dialer := tls.Dialer{Config: c.Transport.(*broker.Transport).TLS}
	conn, err := dialer.DialContext(d.ctx, "tcp", "b1:9092")
	require.NoError(d.t, err)
	defer conn.Close()
	require.NoError(d.t, conn.SetDeadline(time.Now().Add(3*time.Second)))
	wire := protocol.NewConn(conn, "fixture-acl-check")
	wire.SetVersions(map[protocol.ApiKey]int16{protocol.Metadata: 8})
	msg, err := wire.RoundTrip(&metadata.Request{TopicNames: []string{topic}})
	require.NoError(d.t, err)
	r := msg.(*metadata.Response)
	require.Equal(d.t, d.p.ClusterID, r.ClusterID)
	require.Len(d.t, r.Topics, 1)
	require.Equal(d.t, topic, r.Topics[0].Name)
	require.Equal(d.t, int16(broker.TopicAuthorizationFailed), r.Topics[0].ErrorCode)
	_, err = d.produce(c, topic, "forbidden")
	require.Error(d.t, err)
}

func (d drill) commit(c *broker.Client, topic, group string, partition int, offset int64) error {
	r, err := c.OffsetCommit(d.ctx, &broker.OffsetCommitRequest{GroupID: group, GenerationID: -1, Topics: map[string][]broker.OffsetCommit{topic: {{Partition: partition, Offset: offset}}}})
	if err != nil {
		return err
	}
	require.Len(d.t, r.Topics[topic], 1)
	return r.Topics[topic][0].Error
}

func (d drill) checkpoints(c *broker.Client, topic, group string) [3]int64 {
	d.t.Helper()
	r, err := c.OffsetFetch(d.ctx, &broker.OffsetFetchRequest{GroupID: group, Topics: map[string][]int{topic: {0, 1, 2}}})
	require.NoError(d.t, err)
	require.NoError(d.t, r.Error)
	var positions [3]int64
	seen := map[int]bool{}
	for _, p := range r.Topics[topic] {
		require.NoError(d.t, p.Error)
		require.GreaterOrEqual(d.t, p.Partition, 0)
		require.Less(d.t, p.Partition, len(positions))
		require.False(d.t, seen[p.Partition])
		seen[p.Partition] = true
		positions[p.Partition] = p.CommittedOffset
	}
	require.Len(d.t, seen, len(positions))
	return positions
}

// The deployed writers hash notification_4 and delivery_4 to partition zero
// on this reviewed three-partition mapping. The explicit partition-two records
// separately measure acknowledgements, ISR failures and recovered checkpoints.
func (d drill) keyedPublication(role, topic string, offset int64) {
	d.t.Helper()
	transport := d.client(role).Transport.(*broker.Transport)
	p, err := kafka.NewAuthenticatedScopedProducer(kafka.ScopedProducerConfig{Brokers: []string{"b3:9092", "b2:9092", "b1:9092"}, Topic: topic, TLS: transport.TLS})
	require.NoError(d.t, err)
	defer func() { require.NoError(d.t, p.Close()) }()
	require.NoError(d.t, p.VerifyClusterTopic(d.ctx, d.p.ClusterID, 3))
	const scope = "11111111-1111-4111-8111-111111111111"
	at := time.Unix(1700000000, 0).UTC()
	var payload []byte
	var reader string
	if topic == send {
		event := kafka.ScopedSendRequested{SchemaVersion: 1, ApplicationID: scope, OriginEpoch: scope, SourceEpoch: scope, NotificationID: 4, RequestedAt: at, DeadlineAt: at.Add(time.Hour)}
		payload, err = json.Marshal(event)
		require.NoError(d.t, err)
		require.NoError(d.t, p.PublishScopedSend(d.ctx, event))
		reader = "admission"
	} else {
		e := kafka.ScopedDeliveryUpdated{SchemaVersion: 2, ApplicationID: scope, OriginEpoch: scope, SourceEpoch: scope, EventID: scope, DeliveryID: scope, DeliveryVersion: 1, NotificationID: 4, UserID: 1, EventKind: "submission", Provider: "FCM", Status: "SENT", OccurredAt: at}
		payload, err = json.Marshal(e)
		require.NoError(d.t, err)
		require.NoError(d.t, p.PublishScopedDelivery(d.ctx, payload))
		reader = "analytics"
	}
	consumer := d.client(reader)
	group := "fixture-" + reader
	require.Equal(d.t, offset, d.checkpoints(consumer, topic, group)[0])
	read, err := d.read(consumer, topic, 0, offset)
	require.NoError(d.t, err)
	require.Equal(d.t, string(payload), read)
	require.NoError(d.t, d.commit(consumer, topic, group, 0, offset+1))
}

func (d drill) exercise(stage string) {
	for _, scope := range []struct{ topic, writer, reader, group string }{{send, "scheduler", "admission", "fixture-admission"}, {updates, "delivery", "analytics", "fixture-analytics"}} {
		w, r := d.client(scope.writer), d.client(scope.reader)
		cfg := kafka.ScopedProducerConfig{Brokers: []string{"b3:9092", "b2:9092", "b1:9092"}, Topic: scope.topic, TLS: r.Transport.(*broker.Transport).TLS}
		reader, err := kafka.NewGroupLagReader(cfg, d.p.ClusterID, scope.group, 3)
		require.NoError(d.t, err)
		d.t.Cleanup(reader.Close)
		checkLag := func(want ...kafka.GroupLagPartition) {
			d.t.Helper()
			got, err := reader.Read(d.ctx)
			require.NoError(d.t, err)
			require.Equal(d.t, want, got)
		}
		caughtUp := []kafka.GroupLagPartition{{Partition: 0, Valid: true}, {Partition: 1, Valid: true}, {Partition: 2, Valid: true}}
		switch stage {
		case "baseline":
			require.Equal(d.t, [3]int64{-1, -1, -1}, d.checkpoints(d.admin, scope.topic, scope.group))
			checkLag(kafka.GroupLagPartition{Partition: 0}, kafka.GroupLagPartition{Partition: 1}, kafka.GroupLagPartition{Partition: 2})
			denied, err := kafka.NewGroupLagReader(cfg, d.p.ClusterID, "fixture-unrelated", 3)
			require.NoError(d.t, err)
			d.t.Cleanup(denied.Close)
			got, err := denied.Read(d.ctx)
			require.Error(d.t, err)
			require.Nil(d.t, got)
			// A trusted operator initializes every partition of this new, inactive group.
			for partition := range len(d.p.Replicas) {
				require.NoError(d.t, d.commit(d.admin, scope.topic, scope.group, partition, 0))
			}
			offset, err := d.produce(w, scope.topic, "before")
			require.NoError(d.t, err)
			require.EqualValues(d.t, 0, offset)
			checkLag(caughtUp[0], caughtUp[1], kafka.GroupLagPartition{Partition: 2, Valid: true, Lag: 1})
			value, err := d.read(r, scope.topic, 2, offset)
			require.NoError(d.t, err)
			require.Equal(d.t, "before", value)
			require.NoError(d.t, d.commit(r, scope.topic, scope.group, 2, offset+1))
			checkLag(caughtUp...)
			other := send
			if scope.topic == send {
				other = updates
			}
			d.deniedTopic(w, other)
			_, err = d.read(w, scope.topic, 2, 0)
			require.ErrorIs(d.t, err, broker.TopicAuthorizationFailed)
			_, err = d.produce(r, scope.topic, "forbidden")
			require.ErrorIs(d.t, err, broker.TopicAuthorizationFailed)
			require.ErrorIs(d.t, d.commit(r, scope.topic, "fixture-unrelated", 2, 1), broker.GroupAuthorizationFailed)
			request, err := w.CreateTopics(d.ctx, &broker.CreateTopicsRequest{Topics: []broker.TopicConfig{{Topic: "fixture-deny-creation", NumPartitions: 3, ReplicationFactor: 3}}})
			require.NoError(d.t, err)
			require.ErrorIs(d.t, request.Errors["fixture-deny-creation"], broker.TopicAuthorizationFailed)
		case "failover":
			positions := d.checkpoints(r, scope.topic, scope.group)
			require.Equal(d.t, [3]int64{1, 0, 1}, positions)
			checkLag(caughtUp...)
			offset := positions[2]
			value, err := d.read(r, scope.topic, 2, 0)
			require.NoError(d.t, err)
			require.Equal(d.t, "before", value)
			written, err := d.produce(w, scope.topic, "after leader loss")
			require.NoError(d.t, err)
			require.Equal(d.t, offset, written)
			value, err = d.read(r, scope.topic, 2, offset)
			require.NoError(d.t, err)
			require.Equal(d.t, "after leader loss", value)
			require.NoError(d.t, d.commit(r, scope.topic, scope.group, 2, offset+1))
		case "below-isr":
			_, err := d.produce(w, scope.topic, "below minISR")
			require.ErrorIs(d.t, err, broker.NotEnoughReplicas)
		case "recovered":
			require.Equal(d.t, [3]int64{2, 0, 2}, d.checkpoints(r, scope.topic, scope.group))
			checkLag(caughtUp...)
			written, err := d.produce(w, scope.topic, "recovered")
			require.NoError(d.t, err)
			require.EqualValues(d.t, 2, written)
			for i, body := range []string{"before", "after leader loss", "recovered"} {
				v, err := d.read(r, scope.topic, 2, int64(i))
				require.NoError(d.t, err)
				require.Equal(d.t, body, v)
			}
			require.NoError(d.t, d.commit(r, scope.topic, scope.group, 2, 3))
			require.Equal(d.t, [3]int64{2, 0, 3}, d.checkpoints(r, scope.topic, scope.group))
		}
	}
	if stage == "baseline" {
		d.deniedTopic(d.client("stranger"), send)
		d.deniedTopic(d.client("stranger"), updates)
		d.deniedTopic(d.client("requeue"), updates)
		d.keyedPublication("scheduler", send, 0)
		d.keyedPublication("delivery", updates, 0)
	}
	if stage == "failover" {
		d.keyedPublication("requeue", send, 1)
		d.keyedPublication("delivery", updates, 1)
	}
}

func TestSyntheticKafkaDrill(t *testing.T) {
	if os.Getenv("HELLO_KAFKA_DRILL_FIXTURE") != "1" {
		t.Skip("run scripts/kafka-drill.sh with the isolated multi-broker fixture")
	}
	stage := os.Getenv("HELLO_KAFKA_DRILL_STAGE")
	if stage == "" {
		stage = "plan"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	d := drill{t: t, ctx: ctx}
	require.NoError(t, json.Unmarshal(policy, &d.p))
	d.admin = d.client("admin")
	switch stage {
	case "offsets-ready":
		require.Equal(t, [3]int64{-1, -1, -1}, d.checkpoints(d.admin, send, "fixture-admission"))
		require.Equal(t, [3]int64{-1, -1, -1}, d.checkpoints(d.admin, updates, "fixture-analytics"))
		d.layout(3, true)
	case "ready":
		require.Len(t, d.metadata().Brokers, 3)
	case "plan", "check", "apply":
		d.provision(stage)
	case "layout":
		n, err := strconv.Atoi(os.Getenv("HELLO_KAFKA_DRILL_ISR"))
		require.NoError(t, err)
		d.layout(n, false)
	case "baseline", "failover", "below-isr", "recovered":
		d.exercise(stage)
	default:
		t.Fatal("unknown fixture stage")
	}
}
