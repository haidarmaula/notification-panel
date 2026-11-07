package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"hello/internal/dispatch"
	"hello/internal/runloop"
)

// runDelivery joins both loops before the caller drains their FCM and Kafka
// transports. A broker outage can defer publication while ledger work proceeds.
func runDelivery(ctx context.Context, logger *slog.Logger, cfg *deliveryConfig, retry runloop.RetryPolicy, observer runloop.Observer,
	poller *dispatch.DeliveryPoller, outbox *dispatch.UpdateOutbox,
) error {
	deliver := runloop.Task{
		Observer: observer, Name: "delivery_recovery", Interval: cfg.poll, Timeout: time.Hour,
		Run: func(ctx context.Context) error {
			report, err := poller.Tick(ctx)
			if report.Visited > 0 {
				logger.InfoContext(ctx, "delivery poll finished", "campaigns", report.Visited,
					"acquired", report.Acquired, "finalized", report.Finalized, "retired", report.Retired,
					"recorded", report.Recorded, "held", report.Held, "deferred", report.Deferred,
					"unavailable", report.Unavailable)
			}
			return err
		},
	}
	publish := runloop.Task{
		Observer: observer, Name: "delivery_updates", Interval: cfg.poll, Timeout: 30 * time.Second,
		Run: func(ctx context.Context) error {
			for range cfg.pageSize {
				outcome, err := outbox.DrainOne(ctx)
				if err != nil {
					return err
				}
				switch outcome {
				case dispatch.Published:
					continue
				case dispatch.Held:
					return errors.New("delivery update publication held")
				case dispatch.Deferred:
					logger.WarnContext(ctx, "delivery update publication delayed", "outcome", string(outcome))
				}
				return nil
			}
			return nil
		},
	}
	logger.InfoContext(ctx, "delivery worker started")
	return runloop.RunGroup(ctx,
		func(ctx context.Context) error { return runloop.RunTask(ctx, deliver, retry, logger) },
		func(ctx context.Context) error { return runloop.RunTask(ctx, publish, retry, logger) },
	)
}
