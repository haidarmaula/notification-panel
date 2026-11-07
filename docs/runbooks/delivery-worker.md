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

Mount policy and the service-account file under root-owned protected paths.
Use verified DB TLS and the update topic's write/describe Kafka identity.
The worker preflights the actual cluster and partition count, and rechecks its
source and destination when publishing. Immutable topic incarnation is pinned by
the reviewed source manifest. A deployment using a different project, source
or key rotation must provide its corresponding policy.

## Recovery and shutdown

The worker polls immediately and after each completed tick. A single executor
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

For the [Step 40 staging check](../analysis/production-readiness-plan.md#phase-7--production-evidence),
use a test application and owned test device.
Record the deployed commit, application identifier, source incarnation, declared
replica/rate allocation and the test campaign ID. Verify that an already
exhausted device produces one FAILED version with its prior charge intact,
that eligible work resumes after the hold lifts, and that provider acceptance
matches the SENT event. Verify that the campaign closes, the update outbox
drains after Kafka acknowledgement, and unrelated applications keep their work.
Capture safe counts and the worker exit status. Keep keys, addresses and
receipt capabilities out of the acceptance record.
