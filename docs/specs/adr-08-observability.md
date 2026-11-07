# Process diagnostics and operational metrics

The [monitoring runbook](../runbooks/monitoring.md) describes configuration,
triage and tooling. The deployed administrator must supply the target topology,
data retention, approved receiver, thresholds and independent monitoring of
the scraper and alert receiver. The numerical rule thresholds are starting
points for drills. They do not establish a service-level objective.

| Surface | Local contract |
| --- | --- |
| Process health | Cached DB/schema check every 4.5–5.5 seconds, one-second deadline, stale after 15 seconds. Private liveness stays responsive during dependency loss and drain. Unexpected private listener failure cancels business work. |
| Loops | Readiness tracks configured loop start, stop, bounded operation and backoff windows plus two seconds of scheduling allowance. A Kafka admission generation can wait indefinitely for work. A heartbeat measures actual transitions and cannot detect every deadlock. |
| Public HTTP | A new random request ID is returned on each response. Access records carry method, reviewed route template, committed status, body bytes, duration and outcome. Unmatched paths and unrecognized methods map to fixed labels. No raw URL, IP, JWT, body, account or device identifier is logged. A panic before commit returns a generic 500. A panic after commit aborts the connection. |
| Telemetry resources | Two simultaneous metric scrapes, two-second scrape handler deadline. The private server has a two-second header timeout, five-second write timeout and 8 KiB header limit. The HTTP log buffer holds 256 entries. Drops are counted and writes to process pipes and sockets time out. Process counters reset at restart. |
| HTTP and provider | Routes and HTTP status classes have bounded cardinality. The two histograms use eight explicit finite buckets each. The provider reports normalized one-use permit results and elapsed time separately from base HTTP requests, header latency and inflight requests. A transport start is not device receipt. |
| Database | Local pool stats expose capacity, acquired connections and aggregate acquire time. Readiness checks runtime epoch-row visibility and table presence in the current schema. Expensive or failing queries never run in the scrape handler. |
| Durable workload | Scheduler retries age from next eligibility every 15 seconds. A separate 60-second statement snapshot counts scoped campaign and device states and five types of outstanding work, including original outbox age and expired-but-pending intents. Each collector gets a one-second query budget; failure retains previous values and their old success timestamp. Global recording rules take a maximum over fresh replicas per environment. |
| Alerting | Rules cover all configured targets, readiness/DB freshness, stale workload/outbox collectors, aged eligible publication, expired work, provider configuration errors and dropped access records. Grafana has panels for each diagnostic group. Static target removal is detected by reconciling the operator's independent expected inventory. |

The current process counters omit broker-host ISR/quorum/disk signals, complete
Kafka group lag, per-lease live phase timing, external mobile adoption and
distributed span export. Supply verified broker/host collectors, configure the
deployment access/retention policies, test a real alert route and measure
monitoring overhead under the target workload before production sign-off.
