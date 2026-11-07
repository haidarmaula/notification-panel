# Project campaign history

GET /api/v1/apps/{app_id}/campaigns/{campaign_id}/deliveries and
GET /api/v1/apps/{app_id}/campaigns/{campaign_id}/reads require the staff API
key, JWT, notifications.read, an explicit project grant and ACTIVE status.

Both use the project user cursor contract. Missing and foreign campaigns return
404. A visible campaign with no matching records returns an empty page.
Only records with matching project ownership throughout are included.

Delivery fields are id, user_id, device_id, status, sent_at, delivered_at
and opened_at. Read fields are id, user_id and read_at. IDs are decimal
strings and absent timestamps are null. Delivery status is the stored state.
Responses carry Cache-Control: no-store.

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
