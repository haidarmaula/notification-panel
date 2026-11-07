package observability

import (
	"context"
	_ "embed"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

//go:embed workload.sql
var workloadSQL string

type workSample struct {
	count int64
	age   float64
}

// collectWorkload reads one statement snapshot. Failed or partial reads retain the
// preceding complete sample. No account, application or campaign becomes a label.
func (s *State) collectWorkload(ctx context.Context, pool *pgxpool.Pool) {
	counts := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "hello_work_items", Help: "Durable scoped campaigns, delivery intents and waiting work in the latest successful snapshot."}, []string{"family", "kind"})
	ages := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "hello_work_oldest_seconds", Help: "Elapsed age of the oldest outstanding item from its original schedule, deadline, occurrence or quarantine."}, []string{"kind"})
	last := prometheus.NewGauge(prometheus.GaugeOpts{Name: "hello_work_last_success_timestamp_seconds", Help: "Time of the last successful workload snapshot."})
	s.registry.MustRegister(counts, ages, last)
	labels := map[string][]string{
		"campaign": {"DRAFT", "SCHEDULED", "QUEUED", "PROCESSING", "COMPLETED", "FAILED", "CANCELLED"},
		"delivery": {"PENDING", "SENT", "DELIVERED", "OPENED", "FAILED"},
		"waiting":  {"schedule", "expired_delivery", "send_outbox", "update_outbox", "quarantine"},
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		check, cancel := context.WithTimeout(ctx, time.Second)
		samples, err := readWorkload(check, pool)
		cancel()
		if err == nil {
			for family, kinds := range labels {
				for _, kind := range kinds {
					entry := samples[[2]string{family, kind}]
					counts.WithLabelValues(family, kind).Set(float64(entry.count))
					if family == "waiting" {
						ages.WithLabelValues(kind).Set(entry.age)
					}
				}
			}
			last.SetToCurrentTime()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func readWorkload(ctx context.Context, pool *pgxpool.Pool) (map[[2]string]workSample, error) {
	rows, err := pool.Query(ctx, workloadSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	samples := make(map[[2]string]workSample)
	for rows.Next() {
		var key [2]string
		var sample workSample
		if err := rows.Scan(&key[0], &key[1], &sample.count, &sample.age); err != nil {
			return nil, err
		}
		samples[key] = sample
	}
	return samples, rows.Err()
}
