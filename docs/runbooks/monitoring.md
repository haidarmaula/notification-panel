# Notification service monitoring

## Enable private diagnostics

Add the optional `monitoring` object to the protected JSON policy for each
server, admission worker, delivery worker and scheduler instance. Set `listen`
to the instance's private or loopback IP with a nonzero port. Supply PEM strings
in `server_certificate_pem`, `server_key_pem` and `client_ca_pem`. Use a
dedicated monitoring client CA, restrict its issuance and firewall the listener
to the scraper and probes. The server certificate must cover the hostname or IP
used by the scraper. Certificates load once per process. Roll replacement
instances with overlapping trusted roots during rotation.

The role listener serves GET /livez, /readyz and /metrics over mutual TLS 1.3.
Without this optional configuration, there is no diagnostics socket. The public
API and one-shot jobs have separate entry points. Run the following from the
management network using a dedicated client identity.

```sh
curl --fail-with-body --cacert ca.pem --cert scraper.pem --key scraper.key https://server.management.invalid:9095/readyz
```

Replace each example address in [the scrape configuration](../../deploy/observability/prometheus.yml)
with the actual inventory, including every replica. Use a distinct environment
label for each independent data store. Load the rules, choose the approved
Prometheus data source in the Grafana dashboard and configure the team's
alert receiver. Replace relative runbook annotations with authenticated links
in the deployment's rule packaging. The example names under .invalid have no
route. No company receiver, owner or credentials are configured by this project.

## Process and database

Check target inventory and `up` first. A stopped process, a bad certificate and a
missing route can all fail the scrape. A reachable process reports its cached
database/schema check and required loop status through /readyz. The bounded
reasons are `database_stale`, `database_unavailable`, `loop_unavailable` and
`draining`. A sealed deployment can serve safe API operations while dispatch
is held. For a loop failure, inspect the loop's last transition and operation
totals. For a database failure, check TLS, pool occupancy, the current migration
and permissions. Confirm recovery with a fresh successful sample and a ready
role. The probe makes no broker or provider request.

## Durable work

The scheduler samples send and update outbox retry eligibility every 15 seconds.
The minute workload inventory measures retained campaign/device state and
the original age of outstanding schedules, expired pending deliveries,
unresolved quarantine and both outboxes. Inspect a prolonged delay alongside
dispatch holds, scheduler operations, database headroom and broker health.
Confirm both backlog age and count return to expected levels using a fresh
snapshot. During a restore or source incident, keep dispatch sealed while
reconciling offsets and stable event IDs. Follow the
[sealed restore](sealed-restore.md) and [replay](operator-replay.md) procedures.

## Kafka replication

Install the checksum-verified JMX Exporter Java agent on each broker and KRaft
controller. Use the checked-in Kafka MBean allowlist. Bind the agent's HTTP
listener to loopback and expose it through an approved management proxy that
authenticates Prometheus with mTLS. Keep remote JMX and direct agent access
off the management network. The TLS scraper template assumes a verified
proxy hostname, a trusted server CA and a dedicated client identity.
[MBean allowlist](../../deploy/observability/kafka-jmx.yml) ·
[JMX Exporter Java agent](https://prometheus.github.io/jmx_exporter/1.6.0/deployment/java-agent/) ·
[Kafka metrics](https://kafka.apache.org/43/operations/monitoring/).

Add the hello-kafka scrape job and Kafka rule file to the Prometheus instance
used by the application dashboard. Inventory every broker and controller
with distinct addresses and matching environment and cluster labels for
one Kafka cluster. Agree on the alert delays and route alerts to the approved
receiver. A scrape outage alert depends on keeping the expected nodes in the
inventory during maintenance.
[Kafka scraper template](../../deploy/observability/kafka-prometheus.yml) ·
[Kafka alerts](../../deploy/observability/kafka-rules.yml).

- Under-replication indicates lost redundancy. Check replica placement,
  broker reachability and storage.
- Below-minimum ISR means affected partitions cannot accept all-replica writes
  under the configured policy. Keep the durable outbox while resolving the
  broker failure.
- The offline-partition alert reads the active controller. If no active
  controller is observable, check quorum and management scrapes before
  drawing a conclusion about partition state. Missing samples appear as gaps.
- Confirm recovery with a fresh scrape from every configured node, one observed
  active controller, full ISR and reconciled consumer offsets.

[Kafka replication and offset drill](kafka-replication-drill.md).

## Kafka admission checkpoints

With private monitoring enabled, each admission worker samples its configured
group and send topic every 15 seconds. It checks the database source registration,
Kafka cluster identity and partition layout, then reads committed checkpoints
and the broker's earliest and latest consumer-readable offsets. It uses the
worker's existing Kafka identity and neither joins the group nor commits or
resets offsets. Polls use a five-second budget. Topics above 64 partitions
expose an unavailable sample without affecting admission.

- The dashboard reports distance in offsets by partition for fresh, valid
  checkpoints. Zero means a checkpoint equals the sampled readable end.
  Duplicate worker scrapes measure the same group. Compare them individually.
- A fresh invalid range means the checkpoint is missing, precedes the retained
  log or exceeds the readable end. Inspect all partition bounds and the approved
  source and checkpoint records. Follow the [offset drill](kafka-replication-drill.md)
  and [sealed recovery runbook](sealed-restore.md) before any change.
- An unavailable sample omits both lag and range and preserves the last
  successful sample time. Check the hold and source status, broker availability,
  and group/topic read ACLs. A sample older than 45 seconds is stale for alerts
  and dashboards. Range checks use separate reads and can race with retention
  and commits. Reconfirm a suspected breach before taking action.

The downstream delivery-update consumer is owned separately. Verify its
checkpoints and end-to-end notification-path monitoring in the target deployment.
The November 9 race-enabled integration suite verified that the composed
admission worker exported both partitions over its private mTLS listener.

## Provider and receipts

Inspect normalized quota, credential, APNs and sender-mismatch outcomes and
the number of physical transport calls. Check the current app/project binding,
credential rotation and authorized quota allocation. A zero rate during an idle
period is expected. Use campaign statistics for unique provider acceptance,
device reports and user reads. The receipt HTTP panel counts requests, including
retries. Avoid resubmitting an entire campaign after an uncertain result.

The delivery worker reports its local circuit breaker, provider quota cooldown
and permit deferrals by gate. Deferrals occur before an attempt is charged.
The counter measures permit decisions, so repeated decisions for one device
can contribute more than once. A breaker can stay open while waiting for a
successful probe. The quota gauge follows the provider's cooldown deadline.

`HelloProviderDispatchDeferred` warns when a delivery worker repeatedly defers
permits for quota or breaker reasons and the same gate stays active. The local
rule uses an estimated increase of three in two minutes and a 30-second wait.
It requires a fresh, healthy runtime sample and suppresses the warning during
an observed global dispatch hold. Ordinary concurrency deferrals and idle open
gates are visible on the dashboard. Check the affected worker's recent provider
outcomes, actual quota allocation and pending campaign deadlines. Confirm recovery
with a closed gate and successful provider outcomes when work resumes.

## Diagnostic output

A slow log pipe can fill the server's 256-record access buffer or exceed its
250 ms write deadline. Check the local logging driver, disk and collector.
The access counter still records requests that lost their log entry. A derivative
alert can be grouped under a confirmed infrastructure outage. Keep deadline risk
visible, time-limit any silences and record their owner.

## Alert delivery and watchdog

Before activation, ask the on-call owner to identify an approved test receiver
and an independent path for detecting missing monitoring heartbeats.

1. Record the environment, expected scrape inventory, alert route and on-call
   owner. Agree on how the test signal will be triggered and who will observe it.
2. Trigger an approved staging test signal. Record when the rule fired and when
   the receiver got it, with matching severity, scope and runbook. Exercise the
   escalation and maintenance behavior using the agreed test route.
3. Clear the test signal. Verify resolution at the same receiver and reconcile
   the firing and resolved notifications by alert identity.
4. In an approved monitoring drill, interrupt the heartbeat or delivery path.
   Verify that the independent watchdog detects the interruption and reports
   recovery when the path returns. Record both timestamps and the receiver.

The local Kafka drill checks firing and resolution through a disposable webhook
on the internal network. The deployed receiver, escalation and watchdog still
require their own evidence. [Local drill](kafka-replication-drill.md#what-the-drill-verifies).

## Local validation

`bash scripts/check-monitoring.sh` checks scraper syntax, alert rules, firing
and resolution, freshness across duplicate schedulers, and all dashboard
expressions. It provisions the dashboard in an isolated Grafana and reads the
panels back through the HTTP API. Prometheus 3.5.0 and Grafana 12.1.0 are
digest-pinned verification fixtures. Select supported and patched versions
for the target monitoring service. The integration suite tests the real mTLS
listener, healthy sealed database, schema outage, loop failure and drain.
