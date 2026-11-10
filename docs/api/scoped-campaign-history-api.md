# Project campaign history

GET /api/v1/apps/{app_id}/campaigns/{campaign_id}/deliveries and
GET /api/v1/apps/{app_id}/campaigns/{campaign_id}/reads require the staff API
key, JWT, notifications.read, an explicit project grant and ACTIVE status.

Missing and foreign campaigns return 404. A visible campaign with no matching
records returns `{"items":[],"next_after_id":null}` in the response data.
Responses carry `Cache-Control: no-store`.

## Delivery ledger and pagination

Deliveries read only modern `notification_delivery_intents`, strictly scoped to
the application and campaign. Legacy `notification_deliveries` / `device_tokens`
records do not appear, even when they have application attribution.

Delivery fields are `id`, `user_id`, `installation_id`, `status`, `sent_at`,
`delivered_at` and `opened_at`:

- `id` is the delivery intent UUID; `installation_id` is the public installation
  UUID, not an internal BIGINT or legacy device-token ID. `device_id` is removed.
- `user_id` is a decimal string from the intent's frozen `owner_user_id`.
  Installation owner switches or retired bindings do not hide or reattribute
  history to the current owner.
- All stored statuses appear: `PENDING`, `SENT`, `DELIVERED`, `OPENED`, `FAILED`.
- `sent_at` maps to `provider_accepted_at` (provider acknowledgement, not proof of
  device delivery). `delivered_at` and `opened_at` map to
  `observed_delivered_at` and `observed_opened_at`. Absent timestamps are null.
- Provider addresses, provider message IDs, installation proofs, credentials,
  binding internals and raw provider errors are never included.

`limit` defaults to 20 and accepts 1–100. Delivery `after_id`, when supplied,
must be a canonical lowercase, hyphenated, nonzero UUID. Empty, decimal, zero,
uppercase, malformed and repeated cursors return 400; unknown query parameters
are rejected. Omit `after_id` for the zero UUID lower bound. Results use ascending
UUID order (`id > after_id`), **not chronological order**. The query fetches
`limit + 1`; `next_after_id` is the last returned UUID only when another row is
available, otherwise null. Pagination is not a cross-request snapshot; new
random UUIDs below a previously returned cursor can be missed.

Example: `GET /api/v1/apps/{app_id}/campaigns/{campaign_id}/deliveries?limit=1`
returns data shaped as:

```json
{
  "items": [{
    "id": "12345678-abcd-4000-8000-000000000001",
    "user_id": "9007199254740993",
    "installation_id": "12345678-abcd-4000-8000-000000000003",
    "status": "SENT",
    "sent_at": "2026-11-07T08:00:00Z",
    "delivered_at": null,
    "opened_at": null
  }],
  "next_after_id": null
}
```

## Read history (unchanged)

Reads retain the project user **decimal** cursor contract (`after_id` omitted or
`0` starts the page). Read fields are `id`, `user_id` and `read_at`; both IDs are
decimal strings. The delivery UUID cursor does not change this endpoint or any
other list endpoint.

## Modern campaign statistics

`GET /api/v1/apps/{app_id}/campaigns/{campaign_id}/statistics` uses the same
staff permission and explicit project grant. The response data contains
`schema_version: 1`, `snapshot_complete`, `observation_coverage` and `counts`.
Coverage is `reporting_devices_only`. An unprepared or legacy campaign returns
`snapshot_complete: false` with `counts: null`. A prepared empty audience has
a complete snapshot with zero counts.

The counts object reports targeted_users, eligible_users, targeted_devices,
pending_devices, provider_accepted_devices, submission_failed_devices,
observed_devices, opened_devices, opened_users and failed_observed_devices.
The first three are frozen at preparation. Provider acceptance means a recorded
provider acknowledgement. OPENED counts once per device and once per distinct
original user. A failed device with a late report appears in both failure and
observation counts and in the explicit overlap. These counters have different
units and cannot be summed as disjoint delivery outcomes.
