package daemon

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"hello/internal/runloop"
)

var errDeliveryLease = errors.New("delivery runtime lease unavailable")

const deliveryLeaseRefresh = 3 * time.Second

type deliveryLease struct {
	active  bool
	refresh func(context.Context) (bool, error)
}

func (c *Configuration) startDeliveryLease(ctx context.Context, pool *pgxpool.Pool) (*deliveryLease, error) {
	app := c.delivery.application
	scope := func(ctx context.Context) (generation int64, active bool, err error) {
		err = pool.QueryRow(ctx, `SELECT generation,active FROM public.delivery_worker_scope($1)`, app).Scan(&generation, &active)
		return generation, active, err
	}
	generation, _, err := scope(ctx)
	if err != nil || generation < 0 {
		return nil, errDeliveryLease
	}
	if generation == 0 {
		return nil, nil
	}
	instance := pgtype.UUID{Valid: true}
	if _, err := rand.Read(instance.Bytes[:]); err != nil {
		return nil, errDeliveryLease
	}
	instance.Bytes[6] = instance.Bytes[6]&0x0f | 0x40
	instance.Bytes[8] = instance.Bytes[8]&0x3f | 0x80
	refresh := func(ctx context.Context) (bool, error) {
		query, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		var acknowledged bool
		err := pool.QueryRow(query, `SELECT public.acknowledge_application_runtime($1,$2,'DELIVERY',$3,$4)`,
			app, generation, c.policyHash[:], instance).Scan(&acknowledged)
		if err != nil || !acknowledged {
			return false, errDeliveryLease
		}
		current, active, err := scope(query)
		if err != nil || current != generation {
			return false, errDeliveryLease
		}
		return active, nil
	}
	active, err := refresh(ctx)
	if err != nil {
		return nil, err
	}
	return &deliveryLease{active: active, refresh: refresh}, nil
}

func (l *deliveryLease) run(ctx context.Context, work func(context.Context) error) error {
	ready := make(chan struct{})
	if l.active {
		close(ready)
	}
	return runloop.RunGroup(ctx, func(ctx context.Context) error {
		active := l.active
		ticker := time.NewTicker(deliveryLeaseRefresh)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-ticker.C:
				current, err := l.refresh(ctx)
				if err != nil || (active && !current) {
					return errDeliveryLease
				}
				if current && !active {
					active = true
					close(ready)
				}
			}
		}
	}, func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ready:
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return work(ctx)
		}
	})
}
