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
