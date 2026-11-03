# M03b.3a — Project-scoped user and device reads

This persistence contract prepares staff user detail, user listing and device
metadata. The service resolves the internal project ID from current
staff authority and an explicit app grant. These queries take that ID explicitly.
They exclude unassigned legacy rows and provide no fallback to global lookups.

- User detail selects by project and user ID. Missing and foreign rows both
  produce no row. The projection contains ID, external ID, name, email, status
  and timestamps.
- User list orders by ascending user ID and accepts a nonnegative exclusive
  cursor, with zero starting the list. It accepts 1–101 rows so a future service
  can fetch one extra row to detect another page. There is no count or
  cross-request snapshot guarantee.
- Device listing requires project and parent user ID, joins on both, and orders
  by ascending device ID with the same cursor and bound. It selects only device
  ID, platform, active flag, last-seen and timestamps. The active flag describes
  the stored legacy row and does not establish delivery eligibility.

Invalid scope, cursor or limit yields no rows at this persistence boundary.
Database errors propagate to the caller. API validation, user-facing errors
and permission assignments belong to the service and HTTP contract. The
device handler resolves the parent in scope to distinguish missing users
from an empty device list.

These queries back the [staff read endpoints](../api/scoped-users-api.md).
The existing PROVISIONING gate applies to these business routes.
