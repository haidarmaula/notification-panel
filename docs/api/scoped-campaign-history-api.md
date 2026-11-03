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
