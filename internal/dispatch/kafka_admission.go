package dispatch

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	broker "github.com/segmentio/kafka-go"
	"hello/internal/database/sqlc"
)

// KafkaAdmissionConfig contains no credentials. Incarnation is the explicit
// operator-verified generation: pinned kafka-go cannot query the topic UUID.
// Production TLS/SASL policy and process wiring remain separate rollout work.
type KafkaAdmissionConfig struct {
	Brokers     []string
	GroupID     string
	SourceID    pgtype.UUID
	ClusterID   string
	Topic       string
	Incarnation string
}

type KafkaAdmission struct {
	q          *sqlc.Queries
	config     KafkaAdmissionConfig
	preparer   AdmissionPreparer
	quarantine AdmissionQuarantine
}

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
	cfg.Brokers = append([]string(nil), cfg.Brokers...)
	return &KafkaAdmission{q: q, config: cfg, preparer: preparer, quarantine: quarantine}, nil
}

// Run owns the group and joins generation fetchers on every exit. An unsafe
// acknowledgement/source error stops the run for supervised backoff/recreation.
func (r *KafkaAdmission) Run(ctx context.Context) error {
	ctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	group, err := broker.NewConsumerGroup(broker.ConsumerGroupConfig{
		ID: r.config.GroupID, Brokers: r.config.Brokers, Topics: []string{r.config.Topic},
		Dialer: &broker.Dialer{Timeout: time.Second}, Timeout: time.Second,
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
	readers       []*broker.Reader
	wg            sync.WaitGroup
}

func (r *generationAdmissionReader) approved(ctx context.Context) error {
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

func (r *generationAdmissionReader) VerifySource(ctx context.Context) error {
	if err := r.approved(ctx); err != nil {
		return err
	}
	transport := &broker.Transport{DialTimeout: time.Second}
	defer transport.CloseIdleConnections()
	client := &broker.Client{Addr: broker.TCP(r.config.Brokers...), Timeout: 2 * time.Second, Transport: transport}
	metadata, err := client.Metadata(ctx, &broker.MetadataRequest{Topics: []string{r.config.Topic}})
	if err != nil {
		return err
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
	for _, partition := range assigned {
		if !partitions[partition.ID] {
			return ErrQuarantineSource
		}
	}
	// Fetching outlives VerifySource's short call context, but never the generation.
	fetchCtx, cancel := context.WithCancel(r.generationCtx)
	r.cancel = cancel
	r.fetches = make(chan generationFetch, max(1, len(assigned)))
	for _, partition := range assigned {
		reader := broker.NewReader(broker.ReaderConfig{Brokers: r.config.Brokers, Topic: r.config.Topic,
			Partition: partition.ID, QueueCapacity: 1, MinBytes: 1, MaxBytes: 1 << 20,
			MaxWait: 100 * time.Millisecond, ReadLagInterval: -1, Dialer: &broker.Dialer{Timeout: time.Second}})
		if err := reader.SetOffset(partition.Offset); err != nil {
			_ = reader.Close()
			return err
		}
		r.readers = append(r.readers, reader)
		r.wg.Go(func() {
			for {
				message, err := reader.FetchMessage(fetchCtx)
				select {
				case r.fetches <- generationFetch{message, err}:
				case <-fetchCtx.Done():
					return
				}
				if err != nil {
					return
				}
			}
		})
	}
	return nil
}

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

func (r *generationAdmissionReader) Close() {
	if r.cancel != nil {
		r.cancel()
	}
	for _, reader := range r.readers {
		_ = reader.Close()
	}
	r.wg.Wait()
}
