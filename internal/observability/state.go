package observability

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type loopState struct {
	next   time.Time
	active bool
}
type dbSample struct {
	at       time.Time
	ok, hold bool
}

// State is one daemon's private registry. Readiness and scrape handlers use
// cached state only. Loop names come from the process composition root.
type State struct {
	mu              sync.RWMutex
	db              dbSample
	loops           map[string]loopState
	draining        bool
	queues          bool
	accessLogs      bool
	registry        *prometheus.Registry
	httpCount       *prometheus.CounterVec
	latency         *prometheus.HistogramVec
	loopTime        *prometheus.GaugeVec
	operations      *prometheus.CounterVec
	dropped         prometheus.Counter
	transportErrors prometheus.Counter
	logs            chan Request
	kafkaGroup      *kafkaGroupCollector
}

func gauge(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// New allocates independent, finite series for the role's required loops.
func New(names ...string) *State {
	s := &State{registry: prometheus.NewRegistry(), loops: make(map[string]loopState, len(names)), logs: make(chan Request, 256), accessLogs: len(names) == 0}
	for _, name := range names {
		s.loops[name] = loopState{}
		if name == "schedule_promotion" {
			s.queues = true
		}
	}
	s.httpCount = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hello_http_requests_total", Help: "Observed requests by route, status class, outcome and SLO result."}, []string{"method", "route", "status_class", "outcome", "slo_outcome"})
	s.latency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "hello_http_request_duration_seconds", Help: "Elapsed HTTP handling time by route and SLO result.", Buckets: []float64{.005, .025, .1, .3, .5, 1, 5, 15, 30}}, []string{"route", "slo_outcome"})
	s.loopTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "hello_loop_heartbeat_timestamp_seconds", Help: "Last actual loop phase transition."}, []string{"loop"})
	s.operations = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hello_loop_operations_total", Help: "Polling and supervision outcomes, including retries."}, []string{"loop", "outcome"})
	s.dropped = prometheus.NewCounter(prometheus.CounterOpts{Name: "hello_telemetry_dropped_total", Help: "Diagnostic records dropped at the access buffer or its output sink."})
	s.transportErrors = prometheus.NewCounter(prometheus.CounterOpts{Name: "hello_http_server_errors_total", Help: "HTTP server transport diagnostics, including rejected TLS handshakes."})
	s.registry.MustRegister(s.transportErrors, s.httpCount, s.latency, s.loopTime, s.operations, s.dropped,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "hello_role_ready", Help: "One when dependencies and required local loops are ready."}, func() float64 { return gauge(s.reason(time.Now()) == "ready") }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "hello_db_last_check_timestamp_seconds", Help: "Time of the last completed schema and database check."}, func() float64 {
			s.mu.RLock()
			defer s.mu.RUnlock()
			if s.db.at.IsZero() {
				return 0
			}
			return float64(s.db.at.Unix())
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "hello_db_check_healthy", Help: "Whether the latest bounded database and schema check passed."}, func() float64 { s.mu.RLock(); defer s.mu.RUnlock(); return gauge(s.db.ok) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "hello_dispatch_held", Help: "Observed sealed or migration hold; valid only with a fresh healthy DB sample."}, func() float64 { s.mu.RLock(); defer s.mu.RUnlock(); return gauge(s.db.hold) }))
	return s
}

// Phase records bounded operation or idle/backoff windows. An overdue local loop
// fails readiness. Errors on dependencies can still enter a healthy bounded backoff.
func (s *State) Phase(name, outcome string, next time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.loops[name]; !ok {
		return
	}
	s.loops[name] = loopState{next: next, active: outcome != "stopped"}
	s.loopTime.WithLabelValues(name).SetToCurrentTime()
	if outcome == "success" || outcome == "error" {
		s.operations.WithLabelValues(name, outcome).Inc()
	}
}

func (s *State) reason(now time.Time) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.draining {
		return "draining"
	}
	if s.db.at.IsZero() || now.Sub(s.db.at) > 15*time.Second {
		return "database_stale"
	}
	if !s.db.ok {
		return "database_unavailable"
	}
	for _, loop := range s.loops {
		if !loop.active || (!loop.next.IsZero() && now.After(loop.next.Add(2*time.Second))) {
			return "loop_unavailable"
		}
	}
	return "ready"
}

// Dropped records a diagnostic write lost to the local output sink.
func (s *State) Dropped() { s.dropped.Inc() }

// Observe records duration/status synchronously and enqueues a redacted access log.
func (s *State) Observe(r Request) {
	class := "none"
	if r.Status >= 100 && r.Status <= 599 {
		class = strconv.Itoa(r.Status/100) + "xx"
	}
	sloOutcome := requestSLOOutcome(r)
	s.httpCount.WithLabelValues(r.Method, r.Route, class, r.Outcome, sloOutcome).Inc()
	s.latency.WithLabelValues(r.Route, sloOutcome).Observe(r.Duration.Seconds())
	select {
	case s.logs <- r:
	default:
		s.dropped.Inc()
	}
}

func (s *State) logRequests(ctx context.Context, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-s.logs:
			logger.InfoContext(ctx, "http_request", "request_id", r.ID, "method", r.Method, "route", r.Route, "status", r.Status, "bytes", r.Bytes, "duration_seconds", r.Duration.Seconds(), "outcome", r.Outcome)
		}
	}
}

func (s *State) handlers() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("live\n")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		reason := s.reason(time.Now())
		if reason != "ready" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_, _ = w.Write([]byte(reason + "\n"))
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{MaxRequestsInFlight: 2, Timeout: 2 * time.Second, EnableOpenMetrics: true}))
	return mux
}

const readinessQuery = `SELECT sealed OR migration_hold FROM notification_runtime
WHERE singleton AND NOT EXISTS (
    SELECT 1 FROM (VALUES
        ('staff_users', ARRAY['auth_epoch']),
        ('staff_sessions', ARRAY['id','staff_user_id','auth_epoch','session_epoch','refresh_digest','expires_at','revoked_at']),
        ('staff_session_control', ARRAY['session_epoch']),
        ('staff_administration_guard', ARRAY['singleton']),
        ('notification_send_outbox', ARRAY['source_epoch','next_publish_at']),
        ('notification_update_outbox', ARRAY['delivery_version','payload','next_attempt_at']),
        ('notification_delivery_intents', ARRAY['provider_accepted_at','observed_delivered_at','observed_opened_at','audience_route_revision']),
        ('notification_receipt_metadata', ARRAY['delivery_id','key_id','expires_unix']),
        ('notification_receipt_requests', ARRAY['client_event_id','expires_unix']),
        ('notification_observations', ARRAY['delivery_id','event','received_at']),
        ('notification_preparations', ARRAY['prepared_pilot_cohort']),
        ('current_binding_routes', ARRAY['revision','destination','resume_from']),
        ('application_lifecycle_grants', ARRAY['id','applied_at']),
        ('application_dispatch_policy', ARRAY['dispatch_revision','dispatch_mode','dispatch_cohort']),
        ('application_dispatch_grants', ARRAY['id','applied_at'])
    ) AS required(relation_name,column_names)
    CROSS JOIN LATERAL pg_catalog.unnest(required.column_names) AS required_column(name)
    WHERE NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_attribute a
        WHERE a.attrelid=pg_catalog.to_regclass(required.relation_name)
            AND a.attname=required_column.name AND a.attnum>0 AND NOT a.attisdropped
    )
)
AND pg_catalog.to_regprocedure('hold_delivery_dispatch(bigint)') IS NOT NULL
AND pg_catalog.to_regprocedure('apply_application_lifecycle(uuid,uuid)') IS NOT NULL
AND pg_catalog.to_regprocedure('apply_application_dispatch(uuid,uuid)') IS NOT NULL`

func (s *State) collect(ctx context.Context, pool *pgxpool.Pool) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			check, cancel := context.WithTimeout(ctx, time.Second)
			var held bool
			// Catalog checks preserve each daemon's restricted table grants.
			err := pool.QueryRow(check, readinessQuery).Scan(&held)
			cancel()
			s.mu.Lock()
			if err == nil {
				s.db.hold = held
			}
			s.db.ok = err == nil
			s.db.at = time.Now()
			s.mu.Unlock()
			timer.Reset(4500*time.Millisecond + time.Duration(rand.IntN(1000))*time.Millisecond)
		}
	}
}

// Run starts diagnostics on the configured mTLS listener and joins collectors
// before the owner closes its database pool. Business errors retain their identity.
func (s *State) Run(parent context.Context, admin *Admin, pool *pgxpool.Pool, logger *slog.Logger, work func(context.Context) error) error {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	var wg sync.WaitGroup
	// Keep private probes available while the public server drains active requests.
	privateCtx, stopPrivate := context.WithCancel(context.WithoutCancel(ctx))
	defer stopPrivate()
	var adminErr error
	if admin != nil {
		l, err := admin.Listen(ctx)
		if err != nil {
			return err
		}
		wg.Go(func() {
			adminErr = servePrivate(privateCtx, l, s.handlers())
			if adminErr != nil {
				cancel(adminErr)
			}
		})
		s.registry.MustRegister(
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "hello_db_pool_acquired", Help: "Connections checked out from this process pool."}, func() float64 { return float64(pool.Stat().AcquiredConns()) }),
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "hello_db_pool_capacity", Help: "Configured connection ceiling for this process pool."}, func() float64 { return float64(pool.Stat().MaxConns()) }),
			prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "hello_db_pool_acquire_seconds_total", Help: "Accumulated wall time for successful acquisitions since pool creation."}, func() float64 { return pool.Stat().AcquireDuration().Seconds() }),
		)
		wg.Go(func() { s.collect(ctx, pool) })
		if s.kafkaGroup != nil {
			wg.Go(func() { s.kafkaGroup.run(ctx) })
		}
		if s.queues {
			wg.Go(func() { s.collectQueues(ctx, pool) })
			wg.Go(func() { s.collectWorkload(ctx, pool) })
		}
	}
	if s.accessLogs {
		wg.Go(func() { s.logRequests(ctx, logger) })
	}
	stop := context.AfterFunc(ctx, func() { s.mu.Lock(); s.draining = true; s.mu.Unlock() })
	err := work(ctx)
	cancel(nil)
	stopPrivate()
	stop()
	wg.Wait()
	if adminErr != nil {
		return errors.Join(err, adminErr)
	}
	return err
}
