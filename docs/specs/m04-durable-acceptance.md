# M04 durable acceptance

This slice implements scoped immediate acceptance and durable scheduling. Server
transactions perform no Kafka or provider I/O. Production release requires the
existing identity, coverage, routing and infrastructure gates.

A deployment control row persists origin_epoch and source_epoch and starts sealed
with a global migration hold. An explicit per-project dispatch policy starts held
and requires reviewed coverage plus operator-supplied push/key TTL values. Policy
is deployment-owned and has no client override. A missing policy fails closed.
TTL bounds are 1–604800 seconds for this implementation. These are input limits,
not delivery SLOs or approved production defaults.

Immediate acceptance locks deployment control for share, project control for
update, current staff authority, the existing app/actor/source/key row, then the
campaign. Replays require current operation permission and grant. An unexpired
key for the same campaign returns its immutable QUEUED snapshot, even during a
new-send hold. A different campaign using that key returns 409. Expired keys are
removed under the same lock before a new DRAFT claim. App control serializes the
missing-key case. The schema additionally enforces key uniqueness.

New acceptance requires an unsealed deployment, no global/project hold and
reviewed coverage. The conservative local preflight checks all selected ACTIVE
users have an active modern binding and no active legacy token. Unsupported or
unassigned targets fail closed. The all-user BROADCAST meaning is preserved.
M05 must recheck readiness when making its audience snapshot.

One transaction claims DRAFT→QUEUED, stores queued_at, deadline_at and epochs,
inserts one send outbox, stores the key response and writes safe staff audit.
Any failure rolls back all changes. The 202 snapshot includes id, QUEUED status,
queued_at and deadline_at. Idempotency-Key is actor/project scoped. Missing or
empty values derive a campaign-specific key. Duplicate headers and control
characters return 400. A valid explicit key occupies a separate namespace.

Scheduling requires notifications.schedule. One transaction claims DRAFT to
SCHEDULED, persists scheduled_at and its immutable deadline, and records the
current epochs with audit. New schedules honor the acceptance holds. Internal
promotion locks deployment/project then one campaign and rechecks current state.
A due unexpired schedule becomes QUEUED and gains an outbox in one transaction.
Expired schedules become FAILED with ttl_expired and lifecycle evidence. Promotion
preserves the original deadline and may queue during a migration hold, but not a
sealed restore. Legacy schedules lacking the new deadline/epoch evidence require
review and are excluded.

The outbox retains project/campaign identity and epochs. Down refuses accepted
or scheduled records, keys, or pending outbox. The
[bounded outbox dispatcher](m04-outbox-dispatch.md) is implemented as an unwired
library and tested locally. The [runtime composition](m04-m05-runtime-composition.md)
now includes bounded due-schedule discovery/promotion, authenticated admission
and source-bound publication. Executable activation remains open; older publishers
remain retired.
