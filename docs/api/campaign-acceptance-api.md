# Scoped campaign acceptance

Staff API key and Bearer JWT are required. Current database permissions and an
explicit project grant are checked at the route and inside the transaction.
Only ADMIN and SUPER_ADMIN may send or schedule; neither bypasses the grant.
Project IDs are public UUIDs and campaign IDs are positive decimal strings.

| Method | Path | Body | Result |
| --- | --- | --- | --- |
| POST | `/api/v1/apps/{app_id}/campaigns/{campaign_id}/send` | Empty | 202 acceptance snapshot |
| POST | `/api/v1/apps/{app_id}/campaigns/{campaign_id}/schedule` | `{"scheduled_at":"2030-01-02T03:04:05Z"}` | 202 schedule snapshot |

Neither endpoint accepts query parameters. Scheduling requires a future RFC3339
timestamp and strict JSON, with no additional fields. Only DRAFT campaigns may
be accepted or scheduled.

Send accepts one optional `Idempotency-Key` header, at most 255 Unicode
characters without control characters. Missing or empty keys derive a
campaign-specific key in a separate namespace. Keys are scoped by project and
staff actor. Within the deployment-configured retention, the same key and
campaign return the original snapshot; a different campaign returns 409.
`Idempotency-Replayed: true|false` identifies replay. Current authority is still
required for replay, including during a new-send hold. If the project has
enabled the `send_key` maintenance policy, an expired occupied key returns 409
until an authorized retention batch releases it. A fresh key can still be used
for a different eligible DRAFT campaign. [Retention policy](../runbooks/retention.md).

The normal response wrapper contains `data` with string `id`, `status`,
`queued_at`, and `deadline_at`; scheduling substitutes `scheduled_at` for
`queued_at` and returns status SCHEDULED. A 202 means PostgreSQL has committed
the durable acceptance or schedule. It does not mean Kafka, FCM or a device has
received the notification. No provider request occurs in these handlers.

Sealed deployments, migration holds, project holds, or missing dispatch policy
reject new work with 503 and `Retry-After: 5`. Unapproved or incomplete modern
audience coverage returns 409 for immediate sends. Status/key conflicts also
return 409. Invalid inputs return 400. Missing, expired or revoked credentials
return 401, forbidden roles return 403, and unavailable or ungranted projects
return 404. Unknown failures return the
literal `internal server error` with 500. No acceptance state survives a failed
transaction, including audit failure.

Scheduling stores its deadline at acceptance. Internal promotion preserves it;
expired schedules become FAILED with `ttl_expired`, without fabricated device
results. Runtime policy is deployment-owned, starts sealed/held, and is not
editable through these APIs. See the [M04 specification](../specs/m04-durable-acceptance.md)
for transaction and rollout boundaries.
