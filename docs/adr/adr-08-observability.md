# ADR-08 — Process health, delivery visibility and actionable alerting

**Status:** proposed, 2026-11-02. The original proposal is preserved below. The [implementation spec](../specs/adr-08-observability.md) records the locally implemented endpoints, telemetry and alert fixtures. Target operations approval remains open. Evidence checklist:
[observability readiness](../analysis/observability-readiness.md).

**Scope:** shared NEW internal/observability package, HTTP middleware/admin
listeners, server/worker/scheduler/bootstrap lifecycle, instrumented DB/Kafka/
FCM seams, collectors, dashboard/rule/runbook artifacts and configuration.
Existing feature layout stays intact. No public product UI is added here.


Cross-ADR protocol: [consistency review](../analysis/adr-01-11-consistency-review.md).
Its shared lock/identity rules and verification gates apply to this proposal;
review completion does not mean acceptance or runtime implementation.

## Requirements, source evidence and boundaries

Approximately 20,000 active devices and five campaigns/month means idle time
is normal. Kafka is self-hosted on company servers; host count, monitoring
stack and on-call owners are unknown. The project owner approved initial numeric
service objectives in Step 05 on 2026-11-09; platform/operations approval and
target-environment feasibility remain open. Prefer integration with an existing
company monitoring/logging system after inventory; do not silently require a
second stack or assume an alert receiver already exists.

Current internal/middleware/logger.go prints method/raw path only. Server has
no explicit panic wrapper/graceful shutdown; DB.New pings only at startup with
caller context.Background. Worker/scheduler logs are unstructured. No health,
metrics or tracing implementation was found in inspected cmd/middleware files;
go.mod has no direct Prometheus instrumentation dependency. Transitive SDK
telemetry/logging modules do not establish application observability.

ADR-01–07 define durable acceptance, per-device work, receipts, migration
holds and Kafka behavior. Observability diagnoses those contracts; it does
not make a failed transaction successful, provide device receipt guarantees,
or turn a proposed <=60s benchmark into an accepted SLA.

## A-1. Three daemon admin listeners and one-shot job outcomes

### Context

Scrape/auth/probe work should not execute a business request or depend on staff
login. Long-running services and one-shot bootstrap have different lifecycles.

### Decision

NEW private admin listeners on server, worker and scheduler expose GET /livez,
GET /readyz and GET /metrics. Proposed default ports are 9095/9096/9097,
configurable and distinct from current Kafka/API/UI ports. Bind/route only to
the monitoring/load-balancer management network, with ADR-11 authentication/
TLS/firewall policy; never publish unauthenticated diagnostic ports publicly.
Health bodies are bounded non-secret status/reason codes, not raw errors/DSNs.
Do not reuse staff/mobile auth or send synthetic pushes from these routes.

Admin handlers read bounded cached state and process-local metrics; no DB,
broker or provider I/O per probe/scrape. Independent background dependency
checks and collectors have deadlines, jitter and resource limits. Startup
readiness is false until required config/schema checks and fresh DB status
are available. Proposed check cadence 5s, per-call timeout 1s, stale limit 15s;
validate these budgets against real network/DB latency before accepting them.

Bootstrap, requeue and provisioning are one-shot jobs: emit structured start/
finish/exit outcome and deployment/job-run evidence; do not keep an HTTP process
alive solely for health or page on a missing daily bootstrap heartbeat. Alert
when an explicitly expected job run fails or exceeds its agreed deadline.

### Alternatives rejected

Health checks that send FCM messages create side effects. DB queries per scrape
can amplify an outage. One-shot jobs should not look like permanent daemons.

### Consequences

Load balancer/supervisor must be able to reach the correct management listener.
Container loopback and host loopback differ; deployment config must make the
chosen private binding reachable without exposing it to public clients.

## A-2. Liveness, readiness and degradation are different signals

### Context

Restarting a healthy process during an external outage can worsen recovery.
No-traffic periods must not look like a frozen worker.

### Decision

/livez returns 200 when the process/control supervisor is responsive. Dependency
outages, planned holds and an empty queue do not fail liveness. Only a proven
local fatal state/watchdog failure may fail it; do not infer deadlock from time
since last campaign. Independent loop heartbeat/phase timing must distinguish
bounded external wait/backoff from an exited/stuck internal loop.

/readyz answers whether this role can safely perform its designed work, not
whether every downstream service is healthy:

| Role / condition | Readiness | Separate degraded signal |
| --- | --- | --- |
| Server: DB/schema/config healthy, Kafka down | 200; durable outbox-backed API still usable | Kafka/publication backlog reported elsewhere |
| Server: migration delivery hold | 200 for safe API routes; NEW send returns ADR-03's documented 503 | Hold flag/reason and pending deadline risk |
| Worker: DB/required loops healthy, FCM down/breaker open | 200; retain/defer/recover work safely | Provider outage, due backlog and TTL risk |
| Worker: Kafka unavailable, background DB delivery still operational | 200 with degraded reason | Admission connectivity/lag separate from provider activity |
| Scheduler: DB/promotion/dispatcher loops healthy, broker down | 200; can durably promote and defer publication | Send outbox backlog/broker state |
| Any daemon: required DB/schema unusable, critical loop exited or status stale | 503 not_ready | Bounded machine reason and alert |
| Any daemon: graceful drain begun | 503 draining, liveness stays responsive while bounded shutdown runs | Drain state and remaining work |

Dependency checks are observational. A successful TCP connection is not proof
of topic ACLs, group commits, FCM auth/APNs setup or delivery. Do not use
admin-listener 200 or Prometheus up=1 as proof that the role is ready. Unknown/
stale dependency status is explicit, never converted to healthy/zero.

### Alternatives rejected

Making API readiness depend on Kafka defeats durable acceptance. Requiring
FCM success for liveness induces restart storms. A responsive admin goroutine
alone is insufficient evidence that critical work loops are functioning.

### Consequences

Readiness failure is not a command to reset offsets, clear outbox or resend.
Restart/remediation policy is role-specific and tested. The supervisor should
report local loop exit promptly even while HTTP admin remains reachable.

## A-3. Structured diagnostic logs and safe HTTP instrumentation

### Context

Method/path-only logs cannot explain error origin or follow async work.
Raw errors/URLs/identifiers can leak secrets or create excessive telemetry.

### Decision

Use standard-library log/slog JSON for application diagnostic logs. Fields:
UTC timestamp, level, service/environment, deployment/build, event name,
normalized operation/route, request/correlation ID, duration/outcome, finite
reason code and relevant non-secret campaign/delivery/version/lease-generation
or Kafka source position. IDs are diagnostic fields, never metric labels.
Re-use a company log collector if available; pin any new agents/storage.

Generate server request IDs or accept only bounded validated trusted-gateway
IDs; return X-Request-ID. Never trust arbitrary client text as log structure.
Log route templates, not raw URL/query; map unmatched routes and arbitrary
HTTP methods into finite buckets. Do not log body, Authorization, API keys,
JWT, receipt/installation capabilities, addresses, user name/email or unsanitized
provider/SQL errors. Typed bounded error codes replace free-form error dumps.
IP/proxy policy and retention/access controls are ADR-11/10 dependencies.

Middleware captures actual status/bytes/duration, cancellations and panic
outcome while preserving ResponseWriter capabilities via a reviewed wrapper/
ResponseController-compatible Unwrap. Before headers commit, panic recovery
returns the literal internal server error with 500. After headers/body commit,
do not claim a second 500 was sent: abort the partial response safely, record
actual committed status plus panic outcome, and count the operation as failed.
Log bounded safe stack frames/type, not arbitrary panic values containing input.
Worker/background goroutines need their own supervisor/recovery boundaries.

Diagnostic logging uses bounded resources; sample/summarize high-volume success
events and rate-limit repetitive dependency errors. If the log sink stalls,
drop bounded diagnostic messages with a drop/error counter and external alarm
rather than unbounded memory or blocking durable delivery. Preserve DB facts
and existing business audit separately; diagnostic sampling does not redefine
audit durability. Shutdown flush is bounded, not a guarantee of every log line.

### Alternatives rejected

Using a transitive logger only because go.mod contains it is unnecessary.
Logging every token/result is expensive and sensitive. Replacing an already
committed HTTP status with 500 in metrics misrepresents what the client saw.

### Consequences

Shared middleware/logger infrastructure is NEW; update Godoc and verified
M35–M37 findings only when implemented. Audit action constants remain stable.

## A-4. Metric model, ownership and low cardinality

### Context

Counters reset on restart and can miss an update after DB commit/crash. Global
DB counts repeated by replicas must not be summed into inflated business totals.

### Decision

Expose Prometheus/OpenMetrics-compatible metrics using a vetted pinned Go
client (NEW direct dependency if implemented). Use hello_ prefix, base units
and _total for counters, _seconds for durations, _timestamp_seconds for absolute
times. Histograms have reviewed buckets that cover expected latency/deadlines;
only numeric SLO acceptance decides the final bucket boundaries.

Process operation counters/histograms describe executions, including retries,
not unique device success. Durable gauge/snapshot collectors read indexed DB
state under a bounded context periodically (proposed 15s). Collectors publish
last-success timestamp/error/staleness. On DB failure retain a clearly stale
sample or omit its values, never replace counts with zero. Scrapes do not run
the SQL. Account for total collector load across replicas.

Global durable gauges may be exposed by multiple scheduler collectors for HA.
Recording rules select fresh samples and use a documented max/selection across
replicas, **not sum**. Slight sample-time differences are diagnostic estimates;
exact stats and release evidence use consistent DB queries. Freshness guards
must fail visibly when no collector is fresh, not render an all-green empty
dashboard. Per-process counters may be aggregated with reset-aware rate/increase.

Allowed bounded dimensions include role/operation, method bucket, route template,
status class, finite outcome/reason, platform, configured provider/topic/group,
and bounded partition ID. Service/env/instance are scrape labels. Never label
campaign/delivery/user/installation IDs, opaque token, event ID, request/trace ID,
raw path/error, capability or arbitrary client version. Build info is a separate
low-volume static metric, not a label on every latency series. Validate a series
budget and adversarial-input cardinality test in SPEC.

### Alternatives rejected

Summing global gauges from N replicas multiplies backlog. Process send counters
do not prove unique reach. Missing data rendered as zero hides telemetry outages.
Per-user Prometheus labels create unbounded series and privacy problems.

### Consequences

Freshness, scrape health, DB collector load and telemetry drops are monitored
as part of the service. None of these metrics replace authoritative DB audit.

## A-5. Required signal catalog and async correlation

### Context

Silent under-delivery requires visibility at every durable boundary, not only
HTTP uptime. Each metric needs a defined unit/source and consumer.

### Decision

Families below are proposed names/semantics, fixed to exact labels/types in SPEC:

| Signal family | Type / source | Operational purpose |
| --- | --- | --- |
| hello_http_requests_total, hello_http_request_duration_seconds | Process counters/histogram, normalized route/outcome and finite SLO outcome | API errors/latency, including panic and cancellation; valid-request inclusion is explicit |
| hello_role_ready, hello_component_healthy, hello_loop_heartbeat_timestamp_seconds | Cached role/component gauges, finite loop names | Readiness vs local progress vs dependency degradation |
| hello_phase_oldest_active_seconds | Process max gauge per finite phase | Lease renewal/heartbeat cannot hide a hung provider/DB operation |
| hello_db_pool_connections, hello_db_acquire_duration_seconds | pgx pool/instrumentation | Pool saturation and bounded connection wait |
| hello_campaigns, hello_deliveries | Fresh durable gauges by lifecycle state, not IDs | Outstanding/terminal work and explicit zero vs unknown |
| hello_due_deliveries, hello_oldest_due_age_seconds | Fresh DB gauges using effective due time and eligibility | Work that should progress, excluding valid RetryAfter/foreign lease/hold |
| hello_expired_unfinalized_deliveries, hello_oldest_expired_age_seconds | Fresh DB gauges independent of due/deadline-valid filter | Detect abandoned final attempts and expiry hidden from ordinary due scans |
| hello_due_scheduled_notifications, hello_oldest_schedule_delay_seconds | Fresh scheduled-state gauges against scheduled_for/DB time | Detect a dead promotion loop despite no provider traffic |
| hello_outbox_pending, hello_outbox_oldest_age_seconds | Fresh DB gauges by send/update outbox | Publication failure and analytics lag |
| hello_admission_attempts_total, hello_admission_duration_seconds | Process execution count/histogram | Admission retries/quarantine and snapshot cost |
| hello_dead_letters_unresolved, hello_oldest_dead_letter_age_seconds | Durable gauges | Operator-owned poison/replay queue |
| hello_provider_requests_total, hello_provider_request_duration_seconds | Instrument actual SDK transport/logical calls separately | Rate/error/latency and hidden retry amplification |
| hello_provider_inflight, hello_delivery_queue_depth, hello_delivery_queue_capacity | Process gauges | Resource bounds/backpressure |
| hello_retry_deferred_total, hello_lease_conflicts_total, hello_stale_results_total | Process counters, finite reasons | Intentional delay, ownership contention and rejected stale writes |
| hello_provider_breaker_open, hello_migration_dispatch_enabled | Process/cached control state | Distinguish dependency outage and planned hold |
| hello_receipts_total, hello_receipt_delay_seconds | Process request outcome/histogram, not unique delivery proof | Client reporting health, invalid/early/late/duplicate evidence |
| hello_migration_coverage | Fresh durable gauges by finite eligibility/platform | Legacy/unknown/held scope visible without installation labels |
| hello_kafka_group_lag_offsets, hello_kafka_offset_range_valid | Read-only group/partition metadata collector | Committed-offset distance/range, not exact event count or latency |
| hello_collector_last_success_timestamp_seconds, hello_telemetry_dropped_total | Internal freshness/drop signals | Detect the monitoring blind spot itself |

Broker/controller/host exporter signals include offline/under-replicated
partitions, ISR, quorum, disk/inodes, network and ACL/connection failures. Do
not invent existing exporters; inventory/deploy compatible read-only collectors
and scoped credentials. Kafka group lag comes from committed group positions
and log boundaries, not Reader.Lag: pinned kafka-go v0.4.51 returns -1 with
GroupID and ReadLag returns errNotAvailableWithGroup. Unknown is not zero.
Reader.Stats is a delta snapshot since last call: one collector owns it per
reader and accumulates counters correctly; multiple scrapers must not consume
each other's snapshots or export decreasing values as monotonic totals.

Propagate bounded validated request/trace context into the durable outbox and
Kafka headers where supported by the reviewed event contract. No unrestricted
baggage/PII. Delayed retries/recovery use fresh spans with links to source work;
do not hold a request span open for hours or reuse a client ID as authority.
Use OpenTelemetry-compatible context conventions; trace export is sampled,
bounded and nonblocking. Full distributed span coverage is staged after the
logging/metrics baseline; if omitted in v1, declare its coverage explicitly.
Durable request/work IDs still allow diagnostics when traces are sampled out.

### Alternatives rejected

Reader.Lag as zero for a group falsely reports a drained pipeline. TCP ping
does not prove consumer permission or useful progress. A huge forever-open
trace makes recovery dependent on a telemetry backend's lifetime.

### Consequences

Some signals require NEW timestamps/indexed queries and context metadata in
outbox/header handling. Specify schema/query changes with ADR-01/03/05 rather
than adding remote queries or business writes to metrics endpoints.

## A-6. Lifecycle timing, sparse traffic and SLO evidence

### Context

With five campaigns/month, a few minutes without sends is normal. A fast
terminal failure must not count as a successful fast broadcast.

### Decision

Persist authoritative campaign milestones in their state-changing transactions:
queued_at, fanout_complete_at, first submission claim/confirmed response as
distinct facts, initial-disposition completeness where measured, finalized_at,
and scheduled_for for scheduled origin. Per-device acceptance/observations use
ADR-05 evidence; no fabricated provider acceptance from receipt-only success.

Report separate queue-to-initial-disposition, queue-to-terminal, schedule delay,
provider latency, receipt/report delay and update-publication lag. State the
denominator: device submissions, unique eligible bindings, campaigns or HTTP
requests. Timeliness and success must both be measured; excluded devices,
invalid tokens, held work, retries and expiry have explicit categories. A
zero-device campaign is visible, not proof of successful 20,000-device reach.

In-memory histograms are diagnostic and may miss commit-before-crash samples.
Release/pilot reports derive consistent unique outcomes/durations from durable
facts with app/cohort/environment and collection coverage. Record clock source,
restart/reset behavior and excluded/missing data. Synchronize host clocks;
use DB time for cross-process leases and monotonic local timers for durations.

No healthy/no-work alert depends solely on time since last provider success.
Due backlog requires fan-out complete, PENDING, route allowed, retry eligible,
deadline valid and no live foreign lease. Monitor active operation phase age
and expired-but-unfinalized work separately so valid leases or holds cannot
hide a stalled loop. Deadline risk includes intentional holds.

### Alternatives rejected

An empty 5-minute request window is not zero errors out of a meaningful sample.
Counting claims/SDK retries as unique acceptance overstates reach. Measuring
only successful campaign latency hides every failure and long-lived queue.

### Consequences

Initial project-owner-approved Step 05 service targets are staff API
availability of 99.9% per rolling 30 days, p95 latency at or below 300 ms and
p99 at or below 1 second for valid requests. Malformed and unauthorized client
requests are excluded from the availability denominator; server errors and
timeouts count as bad outcomes. Use synthetic checks when traffic is sparse.
For scheduled campaigns not under an explicit hold, 99% should reach durable
queue acceptance within 60 seconds of `scheduled_for` over a rolling 12 months,
with none exceeding 5 minutes; report each campaign because traffic is sparse.
The 20,000-device durable provider-outcome objective is five minutes; 60 seconds
is only a stretch benchmark for that fan-out. These are initial owner targets,
not platform/operations sign-off or evidence of target feasibility. Low-volume
alerts still need absolute impact/age and a monitoring-freshness condition.

## A-7. Alerts, dashboards and operator actions

### Context

An alert without owner/action creates noise; a dashboard cannot notify anyone
if the scraper or notification receiver is down.

### Decision

Propose Prometheus-compatible scraping/rules, Grafana-compatible dashboards
and Alertmanager-style routing, reusing the company stack if suitable. Pin
versions and scope service accounts. Scraper/rule evaluation/notification
receiver availability and a separate watchdog path need deployment evidence;
do not assume monitoring is HA merely because Kafka is. No Slack/email/pager
integration is configured or sent by writing this ADR.

Each alert includes environment/service, finite cause, evidence freshness,
severity, owner/on-call route, runbook link and dashboard. Initial diagnostic
thresholds below are proposals, not numeric SLO or live production rules:

| Condition | Proposed trigger / severity | First operator action |
| --- | --- | --- |
| Expected daemon scrape missing or critical loop exited | Missing >2m / urgent; distinguish startup/maintenance | Check host/process and admin listener, not blind offset reset |
| No fresh DB collector | Stale >45s / warning, escalate with outage duration | Restore telemetry/DB visibility; do not infer empty backlog |
| DB unavailable/pool saturated | Repeated bounded failures >1m / urgent if active work/API impact | Check DB/pool/resource health; preserve work |
| Due work has no progress or excessive phase age | Nonempty actionable backlog + age beyond agreed operation budget / urgent | Inspect leases/phase, rate limit, ownership and recovery |
| Due schedule or expired work not finalized | Nonzero count plus age beyond agreed scheduler/recovery budget / actionable warning or urgent by impact | Inspect promotion/recovery loops and hold policy; preserve original timestamps |
| Pending send/update outbox ages | Oldest >60s / warning; escalation before deadline/retention risk | Check broker/ACL/ISR, dispatcher loops and batch config |
| Kafka quorum/offline partitions/unsafe offset range | Confirmed loss of safe operation / urgent | Follow broker/offset incident runbook; no weaker acks/latest reset |
| Provider auth/APNs/sender mismatch | Confirmed nonretryable configuration error / urgent | Hold affected cohort, verify project/credential/config; no mass token purge |
| Sustained transient/429 or breaker open | Error count plus due backlog/latency / warning or urgent by impact | Reduce/smooth rate, honor RetryAfter and inspect quota/status |
| New quarantine or unexpected campaign expiry/failure | Absolute durable nonzero impact / actionable warning, escalate by campaign | Inspect time window/reason, repair before authorized replay |
| Planned migration hold | Visible info; separate pending deadline/coverage warning | Confirm change owner and resume plan; do not silence TTL risk |
| Invalid receipt/auth surge or ownership conflict | Count/rate with minimum sample and safety impact | Inspect abuse/cohort rollout safely, never log secrets |
| Notification watchdog/log drops/disk pressure | Explicit telemetry/storage failure / warning or urgent | Restore monitoring/log path and capacity; do not delete durable work |

Deduplicate/group related alerts and inhibit derivative provider/lag noise
under a confirmed root infrastructure incident, while keeping user-impact and
deadline warnings visible. Maintenance silences are scoped, timed, owned and
audited. No permanent silence of pipeline alerts during migration.

Required dashboards: role/dependency health; API; campaign/device lifecycle
and due/expired work; send/update outbox plus Kafka; FCM rate/error/breaker;
receipt/coverage; resource/telemetry health. Drill down by authorized logs/DB
filters, not a Prometheus label per campaign. Read-only access is controlled.

### Alternatives rejected

Fixed provider-throughput alerts page continuously during idle months. Ratios
without volume or freshness gates are misleading. Restarting all workers on
provider outage or replaying an entire failed window can duplicate accepted work.

### Consequences

Runbook ownership and actual notification delivery tests are release gates.
Monitoring/notification tests use approved test receivers; no external messages
are authorized implicitly by this design task.

## A-8. Implementation, verification and open decisions

### Context

Observability introduces new operational endpoints/dependencies and can itself
consume resources or fail during an incident.

### Decision

NEW internal/observability contains injected logger/metrics/health state,
bounded background collectors and private admin lifecycle. Instrument shared
HTTP/DB/Kafka/provider seams; keep business outcomes in existing services/DB
transactions. Add only needed direct dependencies and test middleware writer
compatibility/cancellation/panic behavior. No generated SQL hand edits.

NEW deploy/observability artifacts (or company-owned equivalent) include scrape
target inventory, recording/alert rules, dashboard definitions, access policy
and runbook references. Companion SPEC fixes exact files/config names, port/
bind/TLS policy, series/bucket budget, collector cadence/deadlines, retention,
sampling/drop/shutdown bounds and supported tool versions. Static configuration
and promtool rule tests are necessary but do not prove live notifications work.

Fault tests cover process down, admin reachable but worker loop exited, DB/
broker/FCM outage, stale/missing/duplicated collectors, low-volume idle periods,
planned hold/deadline risk, clock skew, telemetry sink outage, high-cardinality
inputs, panic before/after headers and graceful drain. Verify dashboards/rules
against known fixtures and actual deployment topology; run a notification-path
drill separately from production campaign sends.

### Alternatives rejected

Exposing metrics without a scraper/receiver creates no alerting capability.
A healthy collector with wrong denominators is not trustworthy telemetry.
Unbounded logging/tracing must not become a new delivery failure mode.

### Consequences

Still open: company monitoring stack/access/storage, platform/operations
sign-off, on-call and test receiver, numeric alert thresholds and required trace
coverage, retention/security, host time synchronization and endpoint network
policy. The Step 05 service targets have project-owner approval, but require
target-environment feasibility and staging evidence before release. Continue
ADR-09/10/11 design without inventing these facts.

## Primary sources and verification limits

Checked 2026-11-02:
[Prometheus naming](https://prometheus.io/docs/practices/naming/),
[alerting practice](https://prometheus.io/docs/practices/alerting/),
[rule unit testing](https://prometheus.io/docs/prometheus/latest/configuration/unit_testing_rules/),
[OpenTelemetry trace concepts](https://opentelemetry.io/docs/concepts/signals/traces/),
[Go slog](https://pkg.go.dev/log/slog).
Lag and delta-snapshot caveats were verified in pinned kafka-go v0.4.51 reader.go.
No alert/health/provider request, service deployment or observability dependency
installation was executed; existing M35–M38 runtime findings remain open.

## Design verification cases for the companion SPEC

| Input / condition | Expected observable result |
| --- | --- |
| Healthy services, no campaigns for days | Live/ready, empty fresh backlog; no no-send page |
| Kafka down with API DB healthy | API remains ready, acceptance follows outbox/hold contract, publication degradation visible |
| FCM outage or planned hold | No liveness restart storm; safe deferral plus actionable backlog/deadline signals |
| DB lost/stale health cache | Readiness false/unknown, metrics not fabricated zero; admin remains bounded |
| Critical loop exits while admin responds | Component/ready signal fails and alert fires despite up=1 |
| Duplicate durable collectors | Global totals not multiplied; freshness-aware recording rules |
| Reader with GroupID | Lag unavailable through Reader API is not exported as healthy zero |
| Multiple scrapes | One Stats owner; delta counters accumulated, not destructively reread per request |
| Arbitrary URLs/IDs/errors | Bounded series count; sensitive data absent from labels/logs |
| Panic before / after headers | Safe 500 before commit or aborted partial response; actual wire status plus failure outcome recorded |
| Log/trace sink stalls | Bounded resource/drop counters, durable processing continues within tested budgets |
| Early/late/duplicate receipt | Diagnostic request counts distinct from authoritative unique device facts |
| Stale monitoring/notification receiver | Separate telemetry/watchdog failure visible, not an all-green empty dashboard |
| Known fault fixture | Expected alert firing/resolution/inhibition and runbook target verified |
| Graceful drain or one-shot bootstrap | Daemon ready false during drain; job completion/exit tracked without bogus daemon scrape |
'''
