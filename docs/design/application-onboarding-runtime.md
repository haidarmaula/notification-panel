# Onboarding runtime preparation subset

This implements only a delivery planning subset of ONB-05. It does not execute
Docker, reserve capacity, publish server policies or acknowledge readiness.
The complete protected platform profile in
[the onboarding design](application-onboarding.md#protected-profile-and-approval-file-shapes)
remains required before a runtime adapter can deploy anything.

`onboarding.ParseRuntimePolicy` accepts a closed, bounded operator-only JSON
object with these required fields:

| Field | Constraint |
| --- | --- |
| `version` | Integer `1` |
| `environment` | `staging` or `production` |
| `revision` | Positive integer |
| `delivery_image` | Approved repository pinned by a lowercase SHA-256 digest |
| `network` | Approved Docker network name, at most 63 characters |
| `max_applications` | 1–100 |
| `max_overlap` | 1–`max_applications`, including replacement overlap |
| `max_database_connections` | 1–10000, reserved for this delivery allocation |
| `delivery.memory_bytes` | 64 MiB–4 GiB |
| `delivery.nano_cpus` | 100000000–4000000000 |
| `delivery.pids_limit` | 16–512 |
| `delivery.max_database_connections` | 2–10 per replica |

The configured DB budget must cover all application slots plus replacement
overlap. Existing shared server/admission/scheduler allocations must be reserved
separately in the complete platform budget. An absent capacity configuration
fails closed. Counting current allocations and reserving slots atomically remains
the future reconciler's responsibility; an in-memory capacity check is not a
reservation.

The deterministic plan scopes a single delivery replica to canonical app and
configuration UUIDs, environment and a positive generation. Delivery concurrency
and request rate are one; UID/GID are 65532; root filesystem is read-only;
capabilities are dropped and privilege escalation is disabled. It contains no
caller-provided mount, command or environment map.

Shared SERVER, ADMISSION and SCHEDULER acknowledgements are required for the exact
app/configuration/generation/profile revision. Listing these requirements does
not establish that a process loaded that generation or that its source, roles,
mounts, keys and health are approved.

`installation.AppendRuntimeProject` composes the future protected server mobile
policy, preserving existing project objects and key material. It validates both
generations through `LoadRuntime`, rejects project replacement and overlapping
audiences/namespaces, and enforces the protected deployment reader's 64 KiB limit.
Its output contains secrets. No HTTP endpoint exposes it; the future host policy
publisher must enforce authority, immutable publication and restart fencing.
