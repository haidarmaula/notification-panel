package dispatch

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	broker "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
)

// ErrAdmissionCheckpoint holds admission until an operator reconciles the
// broker checkpoint against the reviewed source and retained partition range.
var ErrAdmissionCheckpoint = errors.New("Kafka admission checkpoint requires reconciliation")

// KafkaAdmissionConfig pins source identity, topology and transport policy.
// TLS material and SASL implementations must remain immutable; do not log this
// configuration. Incarnation is operator-approved: SDK metadata omits topic UUIDs.
type KafkaAdmissionConfig struct {
	Brokers        []string
	GroupID        string
	SourceID       pgtype.UUID
	ClusterID      string
	Topic          string
	Incarnation    string
	PartitionCount int32
	TLS            *tls.Config
	SASL           sasl.Mechanism
}

// KafkaAdmission owns group generations and their readers while Run is active.
// Each generation uses serial admission and assignment-bound acknowledgements.
type KafkaAdmission struct {
	q          *sqlc.Queries
	config     KafkaAdmissionConfig
	preparer   AdmissionPreparer
	quarantine AdmissionQuarantine
}

// NewKafkaAdmission retains plaintext compatibility for isolated fixtures.
// Deployed runtimes must use NewAuthenticatedKafkaAdmission. Neither constructor
// connects; Run verifies the registered source and actual cluster metadata.
func NewKafkaAdmission(q *sqlc.Queries, cfg KafkaAdmissionConfig, preparer AdmissionPreparer, quarantine AdmissionQuarantine) (*KafkaAdmission, error) {
	if q == nil || preparer == nil || quarantine == nil || len(cfg.Brokers) == 0 || cfg.GroupID == "" ||
		!cfg.SourceID.Valid || cfg.SourceID.Bytes == [16]byte{} || cfg.ClusterID == "" || cfg.Topic == "" || cfg.Incarnation == "" {
		return nil, errors.New("Kafka admission requires approved source, group and dependencies")
	}
	for _, address := range cfg.Brokers {
		if address == "" {
			return nil, errors.New("Kafka admission requires broker addresses")
		}
	}
	if cfg.PartitionCount < 0 {
		return nil, ErrQuarantineSource
	}
	if cfg.TLS != nil || cfg.SASL != nil {
		transport := kafka.ScopedProducerConfig{Brokers: cfg.Brokers, Topic: cfg.Topic, TLS: cfg.TLS, SASL: cfg.SASL}
		if err := transport.Validate(); err != nil {
			return nil, err
		}
		cfg.TLS = cfg.TLS.Clone()
		if cfg.TLS.RootCAs != nil {
			cfg.TLS.RootCAs = cfg.TLS.RootCAs.Clone()
		}
	}
	cfg.Brokers = append([]string(nil), cfg.Brokers...)
	return &KafkaAdmission{q: q, config: cfg, preparer: preparer, quarantine: quarantine}, nil
}

// NewAuthenticatedKafkaAdmission requires verified TLS, mTLS or SASL identity,
// an explicit partition count and a bounded group ID. Group/coordinator calls,
// metadata verification and partition fetchers all use the same retained policy.
func NewAuthenticatedKafkaAdmission(q *sqlc.Queries, cfg KafkaAdmissionConfig, preparer AdmissionPreparer, quarantine AdmissionQuarantine) (*KafkaAdmission, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return NewKafkaAdmission(q, cfg, preparer, quarantine)
}

// Validate checks authenticated source/group/transport policy without I/O.
func (cfg KafkaAdmissionConfig) Validate() error {
	source := SourceManifest{ID: cfg.SourceID, ClusterID: cfg.ClusterID, Topic: cfg.Topic, Incarnation: cfg.Incarnation, PartitionCount: cfg.PartitionCount}
	if source.Validate() != nil {
		return ErrQuarantineSource
	}
	if cfg.TLS == nil || len(cfg.GroupID) < 1 || len(cfg.GroupID) > 255 || !utf8.ValidString(cfg.GroupID) || strings.TrimSpace(cfg.GroupID) != cfg.GroupID || strings.ContainsFunc(cfg.GroupID, unicode.IsControl) {
		return kafka.ErrScopedTransport
	}
	return (kafka.ScopedProducerConfig{Brokers: cfg.Brokers, Topic: cfg.Topic, TLS: cfg.TLS, SASL: cfg.SASL}).Validate()
}

func (c KafkaAdmissionConfig) dialer() *broker.Dialer {
	return &broker.Dialer{Timeout: time.Second, TLS: c.TLS, SASLMechanism: c.SASL}
}

// Run owns the group and joins generation fetchers on every exit. An unsafe
// acknowledgement/source error stops the run for supervised backoff/recreation.
func (r *KafkaAdmission) Run(ctx context.Context) error {
	ctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	group, err := broker.NewConsumerGroup(broker.ConsumerGroupConfig{
		ID: r.config.GroupID, Brokers: r.config.Brokers, Topics: []string{r.config.Topic},
		Dialer: r.config.dialer(), Timeout: time.Second,
		// kafka-go represents a missing commit with this negative sentinel.
		// VerifySource rejects it before issuing a partition fetch.
		StartOffset: broker.FirstOffset, WatchPartitionChanges: true,
	})
	if err != nil {
		return err
	}
	defer group.Close()
	for {
		generation, err := group.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return err
		}
		generation.Start(func(generationCtx context.Context) {
			runCtx, cancel := context.WithCancel(generationCtx)
			stopParent := context.AfterFunc(ctx, cancel)
			defer stopParent()
			defer cancel()
			reader := &generationAdmissionReader{q: r.q, config: r.config, generation: generation, generationCtx: generationCtx}
			defer reader.Close()
			consumer, err := NewSerialConsumer(reader, r.preparer, r.quarantine)
			if err == nil {
				err = consumer.Run(runCtx)
			}
			if err != nil && generationCtx.Err() == nil && ctx.Err() == nil {
				stop(err)
			}
		})
	}
}

type generationFetch struct {
	message broker.Message
	err     error
}

type generationAdmissionReader struct {
	q             *sqlc.Queries
	config        KafkaAdmissionConfig
	generation    *broker.Generation
	generationCtx context.Context
	cancel        context.CancelFunc
	fetches       chan generationFetch
	transport     *broker.Transport
	wg            sync.WaitGroup
}

func (r *generationAdmissionReader) approved(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	runtime, err := r.q.LockNotificationRuntime(ctx)
	if err != nil {
		return err
	}
	if runtime.Sealed {
		return ErrQuarantineSource
	}
	source, err := r.q.LockSendLogSource(ctx, r.config.SourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrQuarantineSource
	}
	if err != nil {
		return err
	}
	if !source.Active || source.Topic != r.config.Topic || source.ClusterIdentity != r.config.ClusterID || source.TopicIncarnation != r.config.Incarnation {
		return ErrQuarantineSource
	}
	return nil
}

// VerifySource checks source approval and cluster metadata before starting
// fetchers whose lifetime is bounded by the current group generation.
func (r *generationAdmissionReader) VerifySource(ctx context.Context) error {
	if err := r.approved(ctx); err != nil {
		return err
	}
	transport := &broker.Transport{DialTimeout: time.Second, TLS: r.config.TLS, SASL: r.config.SASL, MetadataTopics: []string{r.config.Topic}}
	r.transport = transport
	client := &broker.Client{Addr: broker.TCP(r.config.Brokers...), Timeout: 2 * time.Second, Transport: transport}
	metadata, err := client.Metadata(ctx, &broker.MetadataRequest{Topics: []string{r.config.Topic}})
	if err != nil {
		return err
	}
	if r.config.PartitionCount > 0 {
		if err := kafka.ValidateScopedMetadata(metadata, r.config.ClusterID, r.config.Topic, r.config.PartitionCount); err != nil {
			return ErrQuarantineSource
		}
	}
	if metadata.ClusterID != r.config.ClusterID {
		return ErrQuarantineSource
	}
	partitions := make(map[int]bool)
	for _, topic := range metadata.Topics {
		if topic.Name == r.config.Topic && topic.Error == nil {
			for _, partition := range topic.Partitions {
				if partition.Error == nil {
					partitions[partition.ID] = true
				}
			}
		}
	}
	assigned := r.generation.Assignments[r.config.Topic]
	if len(partitions) == 0 || len(assigned) > 16 {
		return ErrQuarantineSource
	}
	// Read the first batch at the generation's actual numeric checkpoint. The
	// broker returns the live retained bounds on this same fetch. Validate all
	// assignments before handing any partition to the serial consumer.
	initial := make([]*broker.FetchResponse, len(assigned))
	for i, partition := range assigned {
		if !partitions[partition.ID] {
			return ErrQuarantineSource
		}
		if partition.Offset < 0 {
			return ErrAdmissionCheckpoint
		}
		initial[i], err = fetchAdmissionBatch(ctx, client, r.config.Topic, partition)
		if err != nil {
			return err
		}
	}
	fetchCtx, cancel := context.WithCancel(r.generationCtx)
	r.cancel = cancel
	r.fetches = make(chan generationFetch, max(1, len(assigned)))
	for i, partition := range assigned {
		r.wg.Go(func() {
			err := r.streamPartition(fetchCtx, client, partition, initial[i])
			if err != nil {
				select {
				case r.fetches <- generationFetch{err: err}:
				case <-fetchCtx.Done():
				}
			}
		})
	}
	return nil
}

func fetchAdmissionBatch(ctx context.Context, client *broker.Client, topic string, position broker.PartitionAssignment) (*broker.FetchResponse, error) {
	response, err := client.Fetch(ctx, &broker.FetchRequest{
		Topic: topic, Partition: position.ID, Offset: position.Offset,
		MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 100 * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	if errors.Is(response.Error, broker.OffsetOutOfRange) {
		return nil, ErrAdmissionCheckpoint
	}
	if response.Error != nil {
		return nil, response.Error
	}
	if response.Topic != topic || response.Partition != position.ID {
		return nil, ErrQuarantineSource
	}
	if response.LogStartOffset < 0 || position.Offset < response.LogStartOffset || position.Offset > response.HighWatermark {
		return nil, ErrAdmissionCheckpoint
	}
	return response, nil
}

func (r *generationAdmissionReader) streamPartition(ctx context.Context, client *broker.Client, position broker.PartitionAssignment, batch *broker.FetchResponse) error {
	for {
		start := position.Offset
		for {
			record, err := batch.Records.ReadRecord()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			message, err := admissionRecord(record, r.config.Topic, position, start)
			if err != nil {
				return err
			}
			if message == nil {
				continue
			}
			select {
			case r.fetches <- generationFetch{message: *message}:
				position.Offset++
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if position.Offset == start && start < batch.HighWatermark {
			return ErrAdmissionCheckpoint
		}
		next, err := fetchAdmissionBatch(ctx, client, r.config.Topic, position)
		if err != nil {
			return err
		}
		batch = next
	}
}

// A Kafka batch may include records before the requested offset. Beyond that
// prefix every record in this delete-only, non-transactional send log must be
// contiguous. A retention jump or unexpected hole must never advance the group.
func admissionRecord(record *broker.Record, topic string, position broker.PartitionAssignment, batchStart int64) (*broker.Message, error) {
	if record.Key != nil {
		defer record.Key.Close()
	}
	if record.Value != nil {
		defer record.Value.Close()
	}
	if record.Offset < batchStart {
		return nil, nil
	}
	if record.Offset != position.Offset || record.Offset == math.MaxInt64 {
		return nil, ErrAdmissionCheckpoint
	}
	if (record.Key != nil && record.Key.Len() > 1<<20) || (record.Value != nil && record.Value.Len() > 1<<20) {
		return nil, ErrQuarantineInput
	}
	key, err := broker.ReadAll(record.Key)
	if err != nil {
		return nil, err
	}
	value, err := broker.ReadAll(record.Value)
	if err != nil {
		return nil, err
	}
	return &broker.Message{Topic: topic, Partition: position.ID, Offset: record.Offset, Key: key, Value: value}, nil
}

// Fetch rechecks database source approval, then returns a message with an
// acknowledgement bound to its current group generation.
func (r *generationAdmissionReader) Fetch(ctx context.Context) (AdmissionMessage, error) {
	if err := r.approved(ctx); err != nil {
		return AdmissionMessage{}, err
	}
	select {
	case <-ctx.Done():
		return AdmissionMessage{}, ctx.Err()
	case fetched := <-r.fetches:
		if fetched.err != nil {
			return AdmissionMessage{}, fetched.err
		}
		message := fetched.message
		if message.Topic != r.config.Topic || message.Partition < 0 || message.Partition > math.MaxInt32 || message.Offset < 0 || message.Offset == math.MaxInt64 {
			return AdmissionMessage{}, ErrQuarantineSource
		}
		if len(message.Key) > 1<<20 || len(message.Value) > 1<<20 {
			return AdmissionMessage{}, ErrQuarantineInput
		}
		return AdmissionMessage{Source: SourceMessage{LogSourceID: r.config.SourceID, Topic: message.Topic,
			Partition: int32(message.Partition), Offset: message.Offset,
			Key: append([]byte(nil), message.Key...), Payload: append([]byte(nil), message.Value...)},
			Acknowledge: func(ctx context.Context) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if r.generationCtx.Err() != nil {
					return ErrAssignmentLost
				}
				// Generation.CommitOffsets has no context parameter. This owned
				// group's network timeout is 1s; reserve room for that bounded call.
				if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 2*time.Second {
					return context.DeadlineExceeded
				}
				err := r.generation.CommitOffsets(map[string]map[int]int64{message.Topic: {message.Partition: message.Offset + 1}})
				if r.generationCtx.Err() != nil {
					return ErrAssignmentLost
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return err
			}}, nil
	}
}

// Close stops and joins the generation fetchers before closing their transport.
func (r *generationAdmissionReader) Close() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	if r.transport != nil {
		r.transport.CloseIdleConnections()
	}
}
