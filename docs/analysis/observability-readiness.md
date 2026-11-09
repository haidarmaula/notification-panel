# Observability readiness and operator evidence

Date: 2026-11-02. Status: design checklist; no monitoring has been
implemented/installed/tested by this session.
Design: [ADR-08](../adr/adr-08-observability.md).

## Local implementation update

The [implementation spec](../specs/adr-08-observability.md) and [runbook](../runbooks/monitoring.md) supersede the original design-only inventory below. The local rule and dashboard validation uses digest-pinned containers. The monitored Kafka drill verified that Prometheus and Alertmanager delivered a firing notification and its matching resolution to a synthetic webhook. Target receiver, watchdog, broker exporters, access governance, load and SLO evidence remain open.

## Original evidence

- The HTTP logger records only method/path; status/duration/correlation are not yet available.
- Database ping occurs only at startup; the server does not yet have readiness/graceful drain.
- Worker/scheduler use log.Printf; no metrics/health/tracing were found.
- Pinned kafka-go reader: Lag=-1 and ReadLag are unavailable with GroupID;
  Stats is the delta since the last call, not a cumulative counter.
- Campaigns occur about five times/month; idle does not automatically mean the worker is dead.
- The company will self-host Kafka, but the monitoring stack and operators have not
  been inventoried. Do not assume Prometheus/Grafana/receivers already exist.

## Proposed evidence gates

| Gate | Artifact / test evidence | Status |
| --- | --- | --- |
| O01 role probes | Private bind/auth policy, live/ready/degraded contract for server/worker/scheduler | Proposed; not implemented |
| O02 loop supervision | Independent heartbeat/phase timing, exit and shutdown tests | Proposed; not implemented |
| O03 HTTP/log safety | Normalized route/outcome, writer capabilities, panic/header/cancel tests and redaction | Proposed; not implemented |
| O04 metric correctness | Type/label/series/bucket inventory, duplicate collector and reset/freshness fixtures | Proposed; not tested |
| O05 Kafka visibility | Read-only committed offsets/log bounds, ISR/quorum/host exporter compatibility and least privilege | Not installed/tested |
| O06 durable evidence | Lifecycle milestone/schema/query, denominator/coverage and receipt discrepancy reports | Related ADRs proposed |
| O07 dashboards | Role/API/work/backlog/provider/receipt/resource boards with unknown/no-data behavior | Not created |
| O08 rules/runbooks | Numeric triggers, finite severity/owner, promtool firing/resolution/inhibition fixtures | Thresholds proposed only |
| O09 notification path | Actual receiver, escalation/maintenance and separate monitoring watchdog drill | Owner/channel unknown; no message sent |
| O10 resource safety | Log/trace sink outage, metrics collector bounds, scrape/pool and cardinality load | Not tested |
| O11 access/retention | Admin/collector private network, TLS/auth, storage permissions and telemetry lifetime | ADR-10/11 pending |
| O12 sparse-work/SLO | Idle, small denominator, planned hold, stale telemetry and TTL-risk scenarios | Not tested; numeric SLO open |

Actual owner names, evidence links and dates are recorded during implementation/
staging. Receiver tests need designated test recipients and authorization;
writing the ADR does not configure or send a Slack/email/pager notification.

## Operator triage sequence

1. Check telemetry freshness and expected targets before interpreting zero values.
2. Separate process/loop failure from DB/Kafka/FCM degradation or planned hold.
3. Check actionable due/expired work and accepted campaign scope, not time since
   last send alone. Respect RetryAfter, valid leases and route policy.
4. Check outbox oldest age, safe offset range, quarantine and provider evidence.
5. Use restricted logs/DB facts for campaign/delivery diagnosis; never raw token
   dumps or a high-cardinality metric label as a shortcut.
6. Follow the matching runbook/owner: preserve intent, respect original budgets,
   avoid blind replay/offset reset and verify recovery plus alert resolution.

## Boundary

Step 13 is complete as a proposed ADR/signal catalog/checklist. No Go config,
runtime endpoint, metric library, SQL migration, dashboard, live scrape or
alert receiver was changed. Readiness/observability runtime gaps remain open.
Next design work is step 14: ADR-09 API consistency, ADR-10 retention/DR and
ADR-11 security; then step 15 reviews the combined contracts before SPECs.
