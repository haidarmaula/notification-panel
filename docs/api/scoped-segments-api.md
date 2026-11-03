# Project segment reads

Staff API key and JWT are required. Every request checks the current role,
an explicit project grant, and ACTIVE project status. Segment reads use
`segments.read`. Member listing uses `segments.members.read`.

| GET path | Data |
| --- | --- |
| /api/v1/apps/{app_id}/segments | Page of segments |
| /api/v1/apps/{app_id}/segments/{segment_id} | Segment detail |
| /api/v1/apps/{app_id}/segments/{segment_id}/members | Page of members |

Segments expose id, name, nullable description, created_at and updated_at.
Members expose id (membership ID), user_id, external_id, nullable name and
created_at. IDs are decimal strings. Both lists use the same limit, after_id,
items and next_after_id rules as [project user reads](scoped-users-api.md).
Member pagination orders by membership ID.

Missing and foreign segments return 404. A visible segment with no members
returns an empty page. Authorized members of a PROVISIONING project receive
409. Responses carry Cache-Control: no-store.

## Writes

The same authentication, project grant and ACTIVE gate apply to writes.
Segment writes use `segments.write`, and membership writes use
`segments.members.write`.

| Method | Path under /api/v1/apps/{app_id} | Success |
| --- | --- | --- |
| POST | /segments | 201 with segment |
| PUT | /segments/{segment_id} | 200 with replacement segment |
| DELETE | /segments/{segment_id} | 204 |
| PUT | /segments/{segment_id}/members/{user_id} | 204 |
| DELETE | /segments/{segment_id}/members/{user_id} | 204 |

Segment POST and PUT accept a JSON object with name and optional nullable
description. Names are trimmed and follow the registry name validation.
Omitting description in PUT clears it. An exact name conflict in the project
returns 409. DELETE returns 409 if any membership or campaign target still
references the segment. Membership PUT and both DELETE routes require empty
bodies. A duplicate membership returns 409. A missing or foreign parent or
user, or a missing scoped membership on DELETE, returns 404. Write routes
reject query parameters. Mutations and their audit events commit together.
