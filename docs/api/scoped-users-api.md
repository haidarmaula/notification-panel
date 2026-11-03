# Project user reads

All routes require the staff API key, staff JWT, a current database role with
`users.read`, and an explicit active grant for the project UUID in the path.
VIEWER, OPERATOR, ADMIN and SUPER_ADMIN have this read permission. Business
reads require ACTIVE. Current PROVISIONING projects return 409 to authorized
members. Responses carry Cache-Control: no-store.

| GET path | Data |
| --- | --- |
| /api/v1/apps/{app_id}/users | Page of users |
| /api/v1/apps/{app_id}/users/{user_id} | User detail |
| /api/v1/apps/{app_id}/users/{user_id}/devices | Page of device metadata |

User fields are id, external_id, name, email, status, created_at and updated_at.
Device fields are id, platform, is_active, last_seen_at, created_at and updated_at.
The stored active flag does not establish delivery eligibility. Name, email and
last_seen_at may be null. IDs are positive decimal strings.

List responses use the existing response envelope with data.items and
data.next_after_id. The latter is null on the final page. Send its value as
`after_id` to continue in ascending ID order. `after_id` defaults to zero and
`limit` defaults to 20 with a range of 1–100. The cursor does not establish a
snapshot across requests. Unsupported, duplicate or malformed parameters return
400. Detail accepts no query parameters.

Missing and foreign users both return 404, including on the device endpoint.
A visible user with no devices returns an empty page. Unknown or ungranted
projects return 404. Invalid credentials, insufficient authority and
database failures follow the existing staff API error envelope.
