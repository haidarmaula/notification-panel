package onboarding

import (
	"context"
	"crypto/rand"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SharedRuntimeHeartbeat renews the leases approved for the loaded policy and LOGIN.
func SharedRuntimeHeartbeat(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, component string, digest [32]byte) (func(context.Context) error, error) {
	query, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var enabled bool
	err := pool.QueryRow(query, `SELECT coalesce(pg_catalog.has_function_privilege(
		pg_catalog.to_regprocedure('public.acknowledge_shared_application_runtime(text,bytea,uuid)'), 'EXECUTE'),false)`).Scan(&enabled)
	if err != nil || !enabled {
		return nil, err
	}
	instance := pgtype.UUID{Valid: true}
	if _, err := rand.Read(instance.Bytes[:]); err != nil {
		return nil, err
	}
	instance.Bytes[6] = instance.Bytes[6]&0x0f | 0x40
	instance.Bytes[8] = instance.Bytes[8]&0x3f | 0x80
	return func(ctx context.Context) error {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		wasUnavailable := false
		for ctx.Err() == nil {
			query, cancel := context.WithTimeout(ctx, 2*time.Second)
			var renewed int
			err := pool.QueryRow(query, `SELECT public.acknowledge_shared_application_runtime($1,$2,$3)`, component, digest[:], instance).Scan(&renewed)
			cancel()
			if err != nil && !wasUnavailable && ctx.Err() == nil {
				logger.WarnContext(ctx, "onboarding runtime lease renewal unavailable", "component", component)
			}
			wasUnavailable = err != nil
			select {
			case <-ctx.Done():
			case <-ticker.C:
			}
		}
		return context.Cause(ctx)
	}, nil
}
