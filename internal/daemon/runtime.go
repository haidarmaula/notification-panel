package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"hello/internal/database/sqlc"
	"hello/internal/deployment"
	"hello/internal/dispatch"
	"hello/internal/kafka"
	"hello/internal/observability"
	"hello/internal/runloop"
	"hello/internal/scheduler"
)

var (
	errDatabase = errors.New("daemon database startup failed")
	errSource   = errors.New("daemon source startup failed")
	errBroker   = errors.New("daemon broker startup failed")
	errDrain    = errors.New("daemon producer drain failed")
)

// Run opens one pool, verifies source/transport readiness and joins all process
// work before draining its producer and closing the pool. Startup failure closes
// everything acquired. The protected worker mode selects admission or delivery.
func (c *Configuration) Run(ctx context.Context, logger *slog.Logger) (err error) {
	if c == nil || !daemonRole(c.role) || c.database == nil || logger == nil || !deployment.EnvironmentAllowed() {
		return ErrConfiguration
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	startup, cancelStartup := context.WithTimeout(ctx, 15*time.Second)
	defer cancelStartup()
	pool, err := pgxpool.NewWithConfig(startup, c.database.Copy())
	if err != nil {
		return startupFailure(ctx, errDatabase)
	}
	var producer *kafka.ScopedProducer
	defer func() {
		if producer != nil && producer.Close() != nil {
			err = errors.Join(err, errDrain)
		}
		pool.Close()
	}()
	if pool.Ping(startup) != nil {
		return startupFailure(ctx, errDatabase)
	}
	if c.source.VerifyRegistration(startup, pool) != nil {
		return startupFailure(ctx, errSource)
	}
	producer, err = kafka.NewAuthenticatedScopedProducer(c.broker)
	if err != nil {
		return ErrConfiguration
	}
	if producer.VerifyClusterTopic(startup, c.source.ClusterID, c.source.PartitionCount) != nil {
		return startupFailure(ctx, errBroker)
	}
	if c.role == deployment.Worker && c.delivery != nil {
		state := observability.New("delivery_recovery", "delivery_updates")
		poller, client, err := c.delivery.open(startup, pool, state.Provider())
		if err != nil {
			return startupFailure(ctx, ErrConfiguration)
		}
		defer client.Close()
		if c.monitoring != nil {
			state.ObserveProviderAdmission(client)
		}
		outbox, err := dispatch.NewApplicationUpdateOutbox(pool, sourcePublisher{producer, c.source}, c.source, c.delivery.application)
		if err != nil {
			return ErrConfiguration
		}
		cancelStartup()
		return state.Run(ctx, c.monitoring, pool, logger, func(ctx context.Context) error {
			return runDelivery(ctx, logger, c.delivery, c.retry, state, poller, outbox)
		})
	}
	// Startup deadlines must not become the lifetime of a healthy daemon.
	cancelStartup()
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	switch c.role {
	case deployment.Worker:
		// Metadata preflight uses no Produce request. Consumer-only credentials need
		// not have write ACLs; the unused writer is drained before the group starts.
		if producer.Close() != nil {
			producer = nil
			return errDrain
		}
		producer = nil
		q := sqlc.New(pool)
		preparer, err := dispatch.NewPreparer(pool)
		if err != nil {
			return ErrConfiguration
		}
		quarantine, err := dispatch.NewQuarantine(pool)
		if err != nil {
			return ErrConfiguration
		}
		worker, err := dispatch.NewAuthenticatedKafkaAdmission(q, c.admission, preparer, quarantine)
		if err != nil {
			return ErrConfiguration
		}
		logger.WarnContext(ctx, "worker started in admission_only mode", "provider_execution", false)
		state := observability.New("admission")
		if c.monitoring != nil {
			reader, err := kafka.NewGroupLagReader(c.broker, c.source.ClusterID, c.admission.GroupID, c.source.PartitionCount)
			if err != nil {
				return ErrConfiguration
			}
			defer reader.Close()
			state.SetKafkaGroupReader(func(ctx context.Context) ([]kafka.GroupLagPartition, error) {
				if err := c.source.VerifyRegistration(ctx, pool); err != nil {
					return nil, err
				}
				return reader.Read(ctx)
			})
		}
		return state.Run(ctx, c.monitoring, pool, logger, func(ctx context.Context) error {
			return runloop.SuperviseObserved(ctx, "admission", worker.Run, c.retry, logger, state)
		})
	case deployment.Scheduler:
		state := observability.New("schedule_promotion", "send_outbox", "queued_recovery", "replay_cleanup")
		cfg := c.scheduler
		cfg.Observer = state
		runtime, err := scheduler.New(pool, sourcePublisher{producer: producer, source: c.source}, c.source, cfg, logger)
		if err != nil {
			return ErrConfiguration
		}
		logger.InfoContext(ctx, "durable scheduler started")
		return state.Run(ctx, c.monitoring, pool, logger, runtime.Run)
	default:
		return ErrConfiguration
	}
}

type sourcePublisher struct {
	producer *kafka.ScopedProducer
	source   dispatch.SourceManifest
}

func (p sourcePublisher) PublishScopedSend(ctx context.Context, event kafka.ScopedSendRequested) error {
	if err := p.producer.VerifyClusterTopic(ctx, p.source.ClusterID, p.source.PartitionCount); err != nil {
		return err
	}
	return p.producer.PublishScopedSend(ctx, event)
}

func (p sourcePublisher) PublishScopedDelivery(ctx context.Context, payload []byte) error {
	if err := p.producer.VerifyClusterTopic(ctx, p.source.ClusterID, p.source.PartitionCount); err != nil {
		return err
	}
	return p.producer.PublishScopedDelivery(ctx, payload)
}
func startupFailure(ctx context.Context, failure error) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	return failure
}
