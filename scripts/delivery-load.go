//go:build ignore

// Delivery-load runs only in the isolated Docker project created by delivery-load.sh.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	broker "github.com/segmentio/kafka-go"
	"hello/internal/dispatch"
	"hello/internal/installation"
	"hello/internal/kafka"
)

const dsn = "postgresql://integration:integration_only@postgres:5432/notification_integration?sslmode=verify-full&connect_timeout=3&sslrootcert=/etc/hello/ca.crt"
const topic = "notification.delivery.updated"
const cluster = "MkU3OEVBNTcwNTJENDM2Qk"
const namespace = "load-fixture"
const concurrency = 16

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func verify(ok bool, message string) {
	if !ok {
		panic(message)
	}
}

type experiment struct {
	db                           *pgxpool.Pool
	ctx                          context.Context
	app, staff                   int64
	public, source, mode         string
	devices, rate, dbConnections int
}

func (e *experiment) exec(query string, args ...any) {
	_, err := e.db.Exec(e.ctx, query, args...)
	check(err)
}

func (e *experiment) seed() {
	t := time.Now()
	e.exec("ALTER TABLE applications DROP CONSTRAINT applications_status_check")
	check(e.db.QueryRow(e.ctx, "INSERT INTO applications(key,name,status) VALUES('load-fixture','Load fixture','ACTIVE') RETURNING id,public_id::text").Scan(&e.app, &e.public))
	check(e.db.QueryRow(e.ctx, "INSERT INTO staff_users(role_id,name,email,password_hash) SELECT id,'Load','load@fixture.invalid','fixture' FROM roles WHERE name='ADMIN' RETURNING id").Scan(&e.staff))
	e.exec("INSERT INTO users(application_id,external_id) SELECT $1,'load-'||n FROM generate_series(1,$2::int) n", e.app, e.devices)
	e.exec("INSERT INTO mobile_installations(application_id,owner_user_id,proof_verifier) SELECT application_id,id,decode(repeat('11',32),'hex') FROM users WHERE application_id=$1", e.app)
	protector, err := installation.NewProtector(1, map[int32][]byte{1: bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32))
	check(err)
	rows, err := e.db.Query(e.ctx, "SELECT id,owner_user_id,nextval('installation_bindings_id_seq') FROM mobile_installations WHERE application_id=$1 ORDER BY id", e.app)
	check(err)
	defer rows.Close()
	batch := make([][]any, 0, 1000)
	n := 0
	for rows.Next() {
		var inst, user, binding int64
		check(rows.Scan(&inst, &user, &binding))
		n++
		address, err := protector.Protect(installation.BindingIdentity{ApplicationID: e.app, InstallationID: inst, BindingID: binding}, namespace, installation.FCMRegistrationToken, "load-"+strconv.Itoa(n))
		check(err)
		batch = append(batch, []any{binding, e.app, inst, user, int64(1), "ANDROID", namespace, "route", installation.FCMRegistrationToken, address.Fingerprint[:], address.Ciphertext, int32(1)})
		if len(batch) == cap(batch) {
			e.bind(batch)
			batch = batch[:0]
		}
	}
	check(rows.Err())
	e.bind(batch)
	verify(n == e.devices, "missing seeded installation")
	e.exec("INSERT INTO application_dispatch_policy(application_id,hold_new,coverage_approved,push_ttl_seconds,key_ttl_seconds) VALUES($1,false,true,3600,86400)", e.app)
	e.exec("UPDATE notification_runtime SET sealed=false,migration_hold=false")
	check(e.db.QueryRow(e.ctx, "INSERT INTO notification_log_sources(id,topic,cluster_identity,topic_incarnation,active) VALUES(gen_random_uuid(),$1,$2,'load-fixture',true) RETURNING id::text", topic, cluster).Scan(&e.source))
	log.Printf("encrypted_seed devices=%d elapsed=%s", n, time.Since(t))
}

func (e *experiment) bind(batch [][]any) {
	if len(batch) == 0 {
		return
	}
	n, err := e.db.CopyFrom(e.ctx, pgx.Identifier{"installation_bindings"}, []string{"id", "application_id", "installation_id", "owner_user_id", "owner_epoch", "platform", "provider_namespace", "provider_route_key", "address_type", "address_fingerprint", "address_ciphertext", "encryption_key_version"}, pgx.CopyFromRows(batch))
	check(err)
	verify(n == int64(len(batch)), "incomplete address batch")
}

func (e *experiment) prepare() {
	t := time.Now()
	payload, err := json.Marshal(map[string]string{"fixture": "worker-load", "load_mode": e.mode})
	check(err)
	var event kafka.ScopedSendRequested
	event.SchemaVersion, event.ApplicationID = 1, e.public
	check(e.db.QueryRow(e.ctx, `INSERT INTO notifications(application_id,title,body,payload,created_by,status,queued_at,deadline_at,origin_epoch,source_epoch)
		SELECT $1,'Load','Synthetic',$3::jsonb,$2,'QUEUED',clock_timestamp(),clock_timestamp()+interval '55 minutes',origin_epoch,source_epoch FROM notification_runtime
		RETURNING id,queued_at,deadline_at,origin_epoch::text,source_epoch::text`, e.app, e.staff, payload).Scan(&event.NotificationID, &event.RequestedAt, &event.DeadlineAt, &event.OriginEpoch, &event.SourceEpoch))
	e.exec("INSERT INTO notification_targets(application_id,notification_id,target_type) VALUES($1,$2,'BROADCAST')", e.app, event.NotificationID)
	p, err := dispatch.NewPreparer(e.db)
	check(err)
	result, err := p.Prepare(e.ctx, event)
	check(err)
	d := int64(e.devices)
	verify(result == (dispatch.PreparationResult{SnapshotComplete: true, ResolvedUsers: d, EligibleUsers: d, EligibleDevices: d}), "incomplete snapshot")
	log.Printf("prepared campaign=%d devices=%d elapsed=%s", event.NotificationID, d, time.Since(t))
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	check(err)
	return string(b)
}

func (e *experiment) policy() {
	p := map[string]any{
		"mode": "delivery_only", "database_url": dsn,
		"log_source_id": e.source, "cluster_identity": cluster, "topic_name": topic,
		"topic_incarnation": "load-fixture", "partition_count": 1, "retry_min_ms": 100, "retry_max_ms": 1000,
		"kafka": map[string]any{
			"brokers": []string{"kafka:9096"}, "authentication": "MTLS", "ca_pem": readFile("/load-tls/ca.crt"),
			"client_certificate_pem": readFile("/load-tls/client.crt"), "client_key_pem": readFile("/load-tls/client.key"),
			"sasl_username": "", "sasl_password": "",
		},
		"delivery": map[string]any{
			"application_id": e.public, "environment": "fixture", "project_id": "integration-fixture",
			"routes":                []map[string]any{{"namespace": namespace, "key": "route", "platform": "ANDROID", "address_type": installation.FCMRegistrationToken}},
			"credential_project_id": "integration-fixture", "service_account_email": "fixture@integration-fixture.iam.gserviceaccount.com",
			"service_account_file": "/etc/hello/service-account.json", "cross_project_approved": false,
			"lease_seconds": 60, "renew_seconds": 15, "request_timeout_seconds": 10, "retry_floor_seconds": 10, "max_attempts": 3,
			"receipt_key_id": "fixture-key", "observation_window_seconds": 86400, "replica_count": 1,
			"requests_per_second": e.rate, "project_requests_per_second": e.rate, "concurrency": concurrency,
			"breaker_threshold": 5, "breaker_cooldown_seconds": 10, "poll_interval_ms": 100, "page_size": 100,
			"current_key_version": 1, "encryption_keys": []map[string]any{{"version": 1, "key": bytes.Repeat([]byte{1}, 32)}},
			"fingerprint_key": bytes.Repeat([]byte{2}, 32),
			"signing_keys":    []map[string]any{{"id": "fixture-key", "key": ed25519.NewKeyFromSeed(bytes.Repeat([]byte{11}, 32))}},
		},
	}
	if e.dbConnections != 2 {
		p["delivery"].(map[string]any)["database_pool"] = map[string]int{"max_connections_per_replica": e.dbConnections, "deployment_connection_budget": e.dbConnections}
	}
	data, err := json.Marshal(p)
	check(err)
	check(os.WriteFile("/etc/hello/worker.json", data, 0640))
}

func offset(ctx context.Context) int64 {
	conn, err := broker.DialLeader(ctx, "tcp", "kafka:9094", topic, 0)
	check(err)
	defer conn.Close()
	check(conn.SetDeadline(time.Now().Add(5 * time.Second)))
	v, err := conn.ReadLastOffset()
	check(err)
	return v
}

type progress struct{ Sent, Charged, Failed, Campaigns, Outbox, Versions, DeferredUncharged int }

func (e *experiment) progress() progress {
	var p progress
	check(e.db.QueryRow(e.ctx, `SELECT count(*) FILTER(WHERE status='SENT' AND provider_accepted_at IS NOT NULL),coalesce(sum(attempt_count),0),count(*) FILTER(WHERE status='FAILED'),
		(SELECT count(*) FROM notifications WHERE status='COMPLETED'),(SELECT count(*) FROM notification_update_outbox),count(*) FILTER(WHERE delivery_version=1),
        count(*) FILTER(WHERE status='PENDING' AND attempt_count=0 AND attempt_id IS NULL AND next_attempt_at > clock_timestamp()+interval '2 seconds')
		FROM notification_delivery_intents`).Scan(&p.Sent, &p.Charged, &p.Failed, &p.Campaigns, &p.Outbox, &p.Versions, &p.DeferredUncharged))
	return p
}

type providerStats struct {
	LastResponseUTC                                                               time.Time
	Active, Requests, Accepted, Failed, Invalid, Unique, PeakInflight, PeakSecond int
	ArrivalSpanMS, ShortestRetryMS                                                int64
}

func stats(ctx context.Context) providerStats {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://fcm.googleapis.com/__load/stats", nil)
	check(err)
	client := http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	r, err := client.Do(req)
	check(err)
	defer r.Body.Close()
	verify(r.StatusCode == 200, "provider stats unavailable")
	var p providerStats
	check(json.NewDecoder(r.Body).Decode(&p))
	return p
}

func (e *experiment) run(waves int, shape string) {
	e.seed()
	e.policy()
	before := offset(e.ctx)
	started := time.Now()
	prepared := 1
	if shape == "burst" {
		prepared = waves
	}
	for range prepared {
		e.prepare()
	}
	w := exec.Command("/fixture-bin/worker")
	w.Env = []string{"PATH=/usr/bin:/bin"}
	w.Stdout, w.Stderr = os.Stdout, os.Stderr
	check(w.Start())
	done := make(chan struct{})
	var workerErr error
	go func() { workerErr = w.Wait(); close(done) }()
	defer func() {
		select {
		case <-done:
			return
		default:
		}
		_ = w.Process.Signal(syscall.SIGTERM)
		timer := time.NewTimer(12 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
			check(workerErr)
		case <-timer.C:
			_ = w.Process.Kill()
			<-done
			panic("worker shutdown timed out")
		}
	}()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	nextLog := time.Now()
	maxDepth, peakDeferred := 0, 0
	var p progress
	for {
		select {
		case <-done:
			panic(fmt.Sprintf("early worker exit: %v", workerErr))
		case <-e.ctx.Done():
			panic(e.ctx.Err())
		case <-ticker.C:
		}
		p = e.progress()
		maxDepth = max(maxDepth, p.Outbox)
		peakDeferred = max(peakDeferred, p.DeferredUncharged)
		if time.Now().After(nextLog) {
			log.Printf("progress elapsed=%s ledger=%+v sampled_peak_outbox=%d", time.Since(started).Round(time.Second), p, maxDepth)
			nextLog = time.Now().Add(10 * time.Second)
		}
		verify(p.Failed == 0, "unexpected terminal failure")
		if p.Sent != prepared*e.devices || p.Campaigns != prepared || p.Outbox != 0 {
			continue
		}
		if prepared == waves {
			break
		}
		e.prepare()
		prepared++
	}
	elapsed := time.Since(started)
	total := e.devices * waves
	s := stats(e.ctx)
	events := offset(e.ctx) - before
	extra := 0
	if e.mode == "transient" {
		extra = waves
	} else if e.mode == "quota" {
		extra = 1
	}

	var queued time.Time
	var recordedMS, p95MS, p99MS int64
	check(e.db.QueryRow(e.ctx, `SELECT min(n.queued_at),
        round(extract(epoch FROM (max(d.attempt_resolved_at)-min(n.queued_at)))*1000)::bigint,
        round(extract(epoch FROM percentile_disc(0.95) WITHIN GROUP(ORDER BY d.attempt_resolved_at-n.queued_at))*1000)::bigint,
        round(extract(epoch FROM percentile_disc(0.99) WITHIN GROUP(ORDER BY d.attempt_resolved_at-n.queued_at))*1000)::bigint
		FROM notification_delivery_intents d JOIN notifications n ON n.id=d.notification_id`).Scan(&queued, &recordedMS, &p95MS, &p99MS))
	providerMS := s.LastResponseUTC.Sub(queued).Milliseconds()
	verify(providerMS > 0, "missing provider response timing")
	log.Printf("drained devices=%d campaigns=%d shape=%s mode=%s charged=%d broker_updates=%d sent=%d version_one=%d outbox=%d sampled_peak_outbox=%d peak_deferred_uncharged=%d",
		e.devices, waves, shape, e.mode, p.Charged, events, p.Sent, p.Versions, p.Outbox, maxDepth, peakDeferred)
	log.Printf("provider %+v configured_rps=%d concurrency=%d database_connections=%d", s, e.rate, concurrency, e.dbConnections)
	log.Printf("timing first_queue_to_provider_complete_ms=%d first_queue_to_last_result_ms=%d result_p95_ms=%d result_p99_ms=%d first_queue_to_drain=%s avg_devices_per_s=%.2f",
		providerMS, recordedMS, p95MS, p99MS, elapsed, float64(total)/elapsed.Seconds())

	for line := range strings.SplitSeq(readFile(fmt.Sprintf("/proc/%d/status", w.Process.Pid)), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			log.Printf("worker peak resident memory: %s", strings.TrimSpace(strings.TrimPrefix(line, "VmHWM:")))
			break
		}
	}
	verify(events == int64(total) && p.Versions == total, "ledger versions or broker offset delta mismatch")
	e.verifyPublished(before, total)
	verify(s.Requests == total+extra && p.Charged == s.Requests && s.Unique == total && s.Accepted == total && s.Failed == extra && s.Invalid == 0 && s.Active == 0, "physical FCM calls differ from durable attempts")
	verify(s.PeakInflight > 0 && s.PeakInflight <= concurrency, "in-flight cap exceeded")
	// Pacing is measured before the claim. Concurrent DB/network delays can
	// bunch arrivals by at most the already admitted in-flight work.
	verify(s.PeakSecond <= e.rate+concurrency, "provider arrivals exceeded pacing budget plus in-flight allowance")
	if e.mode == "quota" {
		verify(s.ShortestRetryMS >= 59900, "provider quota retry arrived before the one-minute floor")
		if e.devices > 2*concurrency {
			verify(peakDeferred > 0, "no durable uncharged deferral observed during the project quota hold")
		}
	} else if extra > 0 {
		verify(s.ShortestRetryMS >= 9900, "retry arrived before configured floor")
	}
}

// Read after stopping the measurement clock. The per-device map belongs to
// the harness and bounds verification memory by the configured 100k deliveries.
func (e *experiment) verifyPublished(before int64, total int) {
	ctx, cancel := context.WithTimeout(e.ctx, 2*time.Minute)
	defer cancel()
	rows, err := e.db.Query(ctx, "SELECT id::text FROM notification_delivery_intents WHERE application_id=$1", e.app)
	check(err)
	expected, err := pgx.CollectRows(rows, pgx.RowTo[string])
	check(err)
	pending := make(map[string]struct{}, len(expected))
	for _, id := range expected {
		pending[id] = struct{}{}
	}
	verify(len(pending) == total, "unexpected durable delivery count")
	r := broker.NewReader(broker.ReaderConfig{Brokers: []string{"kafka:9094"}, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20})
	defer r.Close()
	check(r.SetOffset(before))
	for range total {
		record, err := r.ReadMessage(ctx)
		check(err)
		event, err := kafka.DecodeScopedDelivery(record.Value)
		check(err)
		_, exists := pending[event.DeliveryID]
		verify(exists && event.ApplicationID == e.public && event.EventKind == "submission" &&
			event.DeliveryVersion == 1 && event.Status == "SENT" && bytes.Equal(record.Key, kafka.DeliveryUpdateKey(event.NotificationID)), "unexpected or repeated Kafka delivery")
		delete(pending, event.DeliveryID)
	}
	log.Printf("verified %d distinct persisted delivery IDs in Kafka", total)
}

func main() {
	log.SetFlags(log.LUTC | log.Ldate | log.Ltime | log.Lmicroseconds)
	verify(len(os.Args) == 7, "run through scripts/delivery-load.sh")
	n, err := strconv.Atoi(os.Args[1])
	check(err)
	waves, err := strconv.Atoi(os.Args[2])
	check(err)
	mode, shape := os.Args[3], os.Args[4]
	rate, err := strconv.Atoi(os.Args[5])
	check(err)
	poolSize, err := strconv.Atoi(os.Args[6])
	check(err)
	verify(n >= 1 && n <= 50000 && waves >= 1 && waves <= 25 && n*waves <= 100000 && poolSize >= 2 && poolSize <= 32 && rate >= 1 && rate <= 9999 && (mode == "healthy" || mode == "transient" || mode == "quota") && (shape == "burst" || shape == "soak"), "fixture parameters out of bounds")
	stopCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// A broken low-volume CI case should fail promptly. Allow pacing at the
	// slowest requested rate and sequential transient retries across waves.
	limit := 50 * time.Minute
	if n*waves <= 256 {
		pacing := time.Duration((n*waves+rate-1)/rate) * time.Second
		limit = min(limit, 2*time.Minute+2*pacing+time.Duration(waves)*15*time.Second)
	}
	ctx, cancel := context.WithTimeout(stopCtx, limit)
	defer cancel()
	db, err := pgxpool.New(ctx, dsn)
	check(err)
	defer db.Close()
	e := experiment{db: db, ctx: ctx, mode: mode, devices: n, rate: rate, dbConnections: poolSize}
	e.run(waves, shape)
}
