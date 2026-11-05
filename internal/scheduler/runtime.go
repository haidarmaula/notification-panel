// Package scheduler composes durable schedule promotion, source-bound outbox
// publication, QUEUED recovery and expired replay-claim cleanup.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"hello/internal/database/sqlc"
	"hello/internal/dispatch"
	"hello/internal/features/applications"
	"hello/internal/runloop"
)

// Config is read once; intervals are polling policy, not delivery SLOs.
type Config struct {
	Interval time.Duration
	PageSize int32
	Retry    runloop.RetryPolicy
}

// Runtime owns four joined polling loops. The caller owns the shared pool and
// authenticated publisher, and must drain the publisher after Run stops before
// closing the pool or handing off the source. It performs no provider calls.
type Runtime struct {
	config       Config
	logger       *slog.Logger
	applications *applications.Service
	outbox       *dispatch.Outbox
	queued       *dispatch.QueuedRecovery
	replay       *dispatch.ReplayRecovery
}

// New constructs all services from one pool/query set without starting I/O.
// The publisher must use verified authenticated transport and compare live broker
// metadata against source before publication. This constructor changes no source,
// epoch, application status or operator-grant authority.
func New(pool *pgxpool.Pool, publisher dispatch.Publisher, source dispatch.SourceManifest, cfg Config, logger *slog.Logger) (*Runtime, error) {
	if pool == nil || publisher == nil || logger == nil || cfg.Interval < 10*time.Millisecond || cfg.Interval > time.Minute || cfg.PageSize < 1 || cfg.PageSize > 100 || cfg.Retry.Validate() != nil {
		return nil, errors.New("invalid scheduler runtime configuration")
	}
	outbox, err := dispatch.NewSourceOutbox(pool, publisher, source)
	if err != nil {
		return nil, err
	}
	q := sqlc.New(pool)
	preparer, err := dispatch.NewPreparer(pool)
	if err != nil {
		return nil, err
	}
	queued, err := dispatch.NewQueuedRecovery(q, preparer)
	if err != nil {
		return nil, err
	}
	replay, err := dispatch.NewReplayRecovery(pool)
	if err != nil {
		return nil, err
	}
	return &Runtime{config: cfg, logger: logger, applications: applications.NewService(q, pool), outbox: outbox, queued: queued, replay: replay}, nil
}

// Run waits until cancellation or a sibling-loop failure and joins every task.
// Polling errors back off independently so maintenance work does not disappear
// when publication is unavailable; every operation still owns its DB/I/O fences.
func (r *Runtime) Run(ctx context.Context) error {
	tasks := []runloop.Task{
		{Name: "schedule_promotion", Run: r.promote},
		{Name: "send_outbox", Run: r.publish},
		{Name: "queued_recovery", Run: r.recoverQueued},
		{Name: "replay_cleanup", Run: r.recoverReplay},
	}
	runs := make([]func(context.Context) error, 0, len(tasks))
	for _, task := range tasks {
		task.Interval = r.config.Interval
		task.Timeout = 30 * time.Second
		runs = append(runs, func(ctx context.Context) error { return runloop.RunTask(ctx, task, r.config.Retry, r.logger) })
	}
	return runloop.RunGroup(ctx, runs...)
}

func (r *Runtime) promote(ctx context.Context) error {
	report, err := r.applications.PromoteDueSchedules(ctx, r.config.PageSize)
	if report.Visited > 0 {
		r.logger.InfoContext(ctx, "schedule promotion progress", "visited", report.Visited, "changed", report.Changed, "held", report.Held)
	}
	return err
}
func (r *Runtime) publish(ctx context.Context) error {
	published := 0
	defer func() {
		if published > 0 {
			r.logger.InfoContext(ctx, "outbox publication progress", "published", published)
		}
	}()
	for range r.config.PageSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		outcome, err := r.outbox.DrainOne(ctx)
		if err != nil {
			return err
		}
		if outcome != dispatch.Published {
			return nil
		}
		published++
	}
	return nil
}
func (r *Runtime) recoverQueued(ctx context.Context) error {
	report, err := r.queued.Tick(ctx, r.config.PageSize)
	if report.Visited > 0 {
		r.logger.InfoContext(ctx, "queued recovery progress", "visited", report.Visited, "prepared", report.Prepared, "terminal", report.Terminal, "held", report.Held, "rejected", report.Rejected)
	}
	return err
}
func (r *Runtime) recoverReplay(ctx context.Context) error {
	report, err := r.replay.Tick(ctx, r.config.PageSize)
	if report.Visited > 0 {
		r.logger.InfoContext(ctx, "replay cleanup progress", "visited", report.Visited, "abandoned", report.Abandoned, "skipped", report.Skipped)
	}
	return err
}
