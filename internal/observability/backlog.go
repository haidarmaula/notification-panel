package observability

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// The scheduler samples global outboxes. Each gauge is the age of the oldest
// currently eligible retry based on the indexed next-attempt time. It is distinct
// from end-to-end time since the original event was committed.
func (s *State) collectQueues(ctx context.Context, pool *pgxpool.Pool) {
	ages := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "hello_outbox_due_age_seconds", Help: "Age of the oldest due send or update row, based on retry eligibility."}, []string{"queue"})
	last := prometheus.NewGauge(prometheus.GaugeOpts{Name: "hello_outbox_last_success_timestamp_seconds", Help: "Time of the last successful durable outbox sample."})
	s.registry.MustRegister(ages, last)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		check, stop := context.WithTimeout(ctx, time.Second)
		var send, update float64
		err := pool.QueryRow(check, `SELECT
            coalesce(extract(epoch FROM statement_timestamp()-(SELECT min(next_publish_at) FROM notification_send_outbox WHERE next_publish_at<=statement_timestamp())),0)::float8,
            coalesce(extract(epoch FROM statement_timestamp()-(SELECT min(next_attempt_at) FROM notification_update_outbox WHERE next_attempt_at<=statement_timestamp())),0)::float8`).Scan(&send, &update)
		stop()
		if err == nil {
			ages.WithLabelValues("send").Set(send)
			ages.WithLabelValues("update").Set(update)
			last.SetToCurrentTime()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
