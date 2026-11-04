# Installation state reconciliation and provider invalidation

The internal provider invalidation operation accepts trusted project, installation,
original binding ID and owner epoch from the stored delivery context. It locks
project, installation and current binding. Only the matching current generation
may retire. A stale or already retired generation returns false without advancing
installation version. A matching invalidation increments version while retaining
owner and owner epoch. It can run while project dispatch is held.

POST /api/v1/apps/{app_id}/mobile/installations/{installation_id}/state accepts
only proof in its JSON body and the project mobile Bearer JWT. A valid proof and
current owner may read installation_id, version, owner_epoch and has_active_binding.
An installation whose owner was cleared by logout may be read by a valid project
user holding its proof. A different current owner returns 404. The endpoint never
returns the stored proof or address. It carries Cache-Control: no-store.

After a response is lost, use state reconciliation with the locally retained
proof before another mutation. Logout can be recognized by an absent binding.
After account switch, the destination account can reconcile the new version.
A lost enrollment response without a retained proof still requires fresh origin
evidence and the reviewed recovery process. Proof knowledge remains mandatory.

The provider entry point is internal to the backend. Its caller must use the
immutable delivery binding identity and epoch. Provider result mapping and its
execution hook belong to M06.
