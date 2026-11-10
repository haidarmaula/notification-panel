# Run and verify the scoped delivery worker

Run a dedicated `cmd/worker` process with `mode: "delivery_only"` for each
application being served. The Kafka admission process uses `admission_only`
and prepares the audience in PostgreSQL. Both roles read the protected
`/etc/hello/worker.json` in their own deployment (on Darwin,
`/private/etc/hello/worker.json`). They can run independently.

## Deployment policy

Use the [executable integration fixture](../../tests/integration/delivery_worker_test.go)
for a complete example of the JSON structure. Its values belong to the
disposable test environment. Provision the following values from the target
deployment's approved identities and secret store.

| Location | Required settings |
| --- | --- |
| Top level | Set `mode` to `delivery_only`. Supply the verified-TLS `database_url`, the registered **delivery update** source's `log_source_id`, `cluster_identity`, `topic_name`, `topic_incarnation` and `partition_count`, authenticated `kafka` transport, and `retry_min_ms` / `retry_max_ms` for loop failures. Only the admission role takes `group_id`. |
| `delivery` scope | Set the public `application_id`, `environment`, target Firebase `project_id`, and `routes`. Each route specifies `namespace`, `key`, `platform` and `address_type` matching a registered installation binding. Discovery and update publication select only this application. |
| `delivery` provider credential | Supply the absolute `service_account_file` path, `credential_project_id`, `service_account_email` and `cross_project_approved`. The credential's project and email must match. Cross-project IAM grants require explicit policy approval. |
| `delivery` keys | Supply `current_key_version`, `encryption_keys` entries with `version` and `key`, plus `fingerprint_key` from installation enrollment. Address and fingerprint keys are 32 bytes. Supply the active `receipt_key_id`, `signing_keys` entries with `id` and full 64-byte Ed25519 private `key`, and `observation_window_seconds`. Encode key bytes in canonical padded base64. Retain keys used by pending deliveries. |
| `delivery` limits | Specify `poll_interval_ms`, `page_size`, `lease_seconds`, `renew_seconds`, `request_timeout_seconds`, `retry_floor_seconds`, `max_attempts`, `breaker_threshold`, `breaker_cooldown_seconds`, `concurrency`, `requests_per_second`, `project_requests_per_second` and `replica_count`. Allocate rates across all clients sharing the provider project and enforce the declared replica count in deployment. |
| Optional `delivery.database_pool` | The default maximum is two PostgreSQL connections per worker, with zero reserved idle connections. To allocate a larger pool, set both `max_connections_per_replica` (2–32) and `deployment_connection_budget`. The maximum multiplied by the declared `replica_count` must fit that deployment allocation. Account separately for other roles and database clients, including overlapping rollout replicas. |

Mount policy and the service-account file under root-owned protected paths.
Use verified DB TLS and the update topic's write/describe Kafka identity.
The worker preflights the actual cluster and partition count, and rechecks its
source and destination when publishing. Immutable topic incarnation is pinned by
the reviewed source manifest. A deployment using a different project, source
or key rotation must provide its corresponding policy.

## Register the delivery update source

Source registration is an owner operation because the application cannot prove
that a topic name still identifies the approved Kafka log. First provision the
topic and ACLs on the approved cluster. Through its authenticated Kafka admin
connection, independently record the cluster identity, exact topic incarnation
ID and partition count. Store those values with the source UUID, owner, purpose
and review reference in the deployment's restricted evidence system. Do not
derive a new incarnation from the topic name or reuse the old registration after
topic recreation.

As the trusted database schema owner, insert the reviewed identity inactive:

```sql
INSERT INTO public.notification_log_sources
       (id, topic, cluster_identity, topic_incarnation, active)
VALUES (:'source_id'::uuid, :'topic', :'cluster_identity', :'topic_incarnation', false);
```

Re-read the row and compare every value with the approved evidence and live
broker metadata. A separate authorized reviewer then approves activation; the
schema owner applies the exact-row transition:

```sql
UPDATE public.notification_log_sources
   SET active = true
 WHERE id = :'source_id'::uuid
   AND topic = :'topic'
   AND cluster_identity = :'cluster_identity'
   AND topic_incarnation = :'topic_incarnation'
   AND active = false
RETURNING id, topic, cluster_identity, topic_incarnation, active;
```

Require exactly one returned row. If it returns none, stop and reconcile the
database and evidence; do not widen the predicate. Put the same reviewed source
UUID, cluster identity, topic, incarnation, and partition count in the
protected worker policy. Startup checks compare the active database row and
live broker metadata. Deactivation is a separate owner action after stopping
dependent workers and coordinating readers, checkpoints, and any source
handoff. Preserve old registrations and attributed evidence for recovery.

## Recovery and shutdown

The worker polls immediately and waits after each completed tick. The optional
`delivery.active_poll_interval_ms` ranges from 10 ms through `poll_interval_ms`
and defaults to that normal interval. Acknowledged progress with another device
or campaign page indicated, or a fully published outbox page, selects the active
interval. Idle and held-only work uses the normal interval. Errors use bounded
retry backoff. The heartbeat records the selected next wake time.

A single executor
owns a campaign lease while processing a bounded page. It renews during work
and joins in-flight sends before releasing the lease. It records the FCM outcome
under the attempt's lease generation. If the write fails, it retries the same
result for up to ten seconds while the generation is valid. A failure to publish
leaves the versioned event in the update outbox while the worker continues
processing eligible devices.

Each claim stores the later of its configured recovery floor and an exponential
retry delay. The first delay is ten seconds. Successive windows double up to
five minutes, with stable jitter between half the window and its ceiling,
subject to a ten-second minimum. Quota results wait at least one minute.
The saved floor and a longer provider Retry-After always take precedence. A
breaker deferral before a claim updates eligibility without consuming an
attempt. Lease takeover preserves charged attempts and can retire them after
budget or deadline exhaustion. See the [recovery contract](../specs/m06-recovery.md).

On SIGTERM the process stops new work, joins provider/lease operations, drains
the Kafka producer and closes its database pool. An abrupt kill leaves a finite
lease and the persisted retry floor. A push accepted by FCM just before the
worker loses its response can still be submitted again within budget. A
`SENT` update records acceptance by the provider.

## Acceptance

From the repository root, run `GOFLAGS=-race bash scripts/integration.sh` to
exercise the isolated deployment. The fixture uses its own PostgreSQL schema,
Kafka source, application and synthetic provider identity. It seeds an expired
lease with one abandoned charge and another eligible device, starts the worker,
and releases the dispatch hold. The runner owns fixture teardown.

On 2026-11-07 the race-enabled integration suite passed. The Linux worker
preserved the abandoned charge, emitted its FAILED update and made exactly one
FCM request for the eligible device. It published the SENT update, closed the
campaign, and left the other application's event in the outbox. SIGTERM exited
cleanly with zero remaining worker database connections. Separate PostgreSQL
cases verified deadline expiry, quota deferral across restart, and continued
delivery while the database repeatedly rejected an unrelated device's update.

### Step 40 staging prerequisites

1. Provision an isolated staging backend with PostgreSQL, Kafka, the gateway,
   server, scheduler, admission worker and delivery worker. Apply the migrations,
   install the scoped database grants, and provision the two Kafka topics,
   source registrations and role-specific TLS identities. Use an empty test
   application and keep dispatch held during setup.
2. Register the staging Android package and iOS bundle in a Firebase test
   project. Enable FCM, configure APNs in Firebase for the test iOS app, and
   give the staging sender permission to send to the selected project. Mount
   the service-account credential and backend keys from the approved secret
   store. Record only public identifiers and secret references in the handoff.
3. Configure a staging Laravel issuer and one-use origin issuer with the
   application's agreed signing, claims and audience policy. Use test accounts
   whose subjects map to users in the staging application.
4. Build and sign the staging React Native app with FCM registration,
   installation enrollment and receipt reporting. Provide at least one owned
   physical Android device and one owned physical iPhone. Exclude the test
   installations from overlapping OneSignal campaigns and grant only the
   reviewed staging cohort access to FCM dispatch.

Before the first `admission_only` worker starts, initialize its Kafka consumer
group to an owner-reviewed numeric offset. A new group has no committed
checkpoint; the worker rejects Kafka's negative `FirstOffset` sentinel and
restarts rather than guessing a starting point. With the group inactive, use
the authenticated Kafka admin identity to inspect the topic's retained bounds
and review the intended offset. Dry-run and then apply the exact checkpoint:

```sh
kafka-consumer-groups.sh --bootstrap-server "$BROKER" \
  --command-config "$ADMIN_TLS_CONFIG" --reset-offsets --group "$GROUP" \
  --topic "$TOPIC:$PARTITION" --to-offset "$APPROVED_OFFSET" --dry-run
kafka-consumer-groups.sh --bootstrap-server "$BROKER" \
  --command-config "$ADMIN_TLS_CONFIG" --reset-offsets --group "$GROUP" \
  --topic "$TOPIC:$PARTITION" --to-offset "$APPROVED_OFFSET" --execute
```

Confirm the committed offset and lag before starting the worker. For a new,
verified-empty smoke topic, offset zero is the expected starting point. Never
reset a populated deployment group to zero without reconciling its prior
processing and retention state.

See the [device and cutover evidence register](../analysis/cutover-readiness.md)
and the [backend cutover controls](application-dispatch.md).

### Step 40 staging acceptance

Use the test application and owned devices. Verify foreground, background and
terminated receipt behavior where supported, tap handling, token refresh and
logout or account switching. Record app observations separately from FCM
acceptance.
Record the deployed commit, application identifier, source incarnation, declared
replica/rate allocation and the test campaign ID. Verify that an already
exhausted device produces one FAILED version with its prior charge intact,
that eligible work resumes after the hold lifts, and that provider acceptance
matches the SENT event. Verify that the campaign closes, the update outbox
drains after Kafka acknowledgement, and unrelated applications keep their work.
Capture safe counts and the worker exit status. Keep keys, addresses and
receipt capabilities out of the acceptance record.
