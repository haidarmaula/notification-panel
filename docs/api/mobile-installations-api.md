# Mobile installation lifecycle

Mount these routes by setting MOBILE_POLICY_FILE to an operator-managed JSON
file. The server rejects invalid configuration at startup. The file contains
encryption keys and must be restricted to the server identity.

| POST path under /api/v1/apps/{app_id}/mobile/installations | Required JSON fields | Success |
| --- | --- | --- |
| collection | platform, address_type, address, origin | 201 with state and proof |
| /{installation_id}/refresh | proof, version, platform, address_type, address | 200 with state |
| /{installation_id}/logout | proof, version | 200 with state |
| /{installation_id}/switch | proof, version, platform, address_type, address | 200 with state |

All routes use one Bearer mobile JWT. No staff API key is sent. The switch JWT
represents the destination user. Version and owner_epoch are decimal strings.
State includes installation_id, version and owner_epoch. The client stores the
one-time enrollment proof securely. An invalid JWT or origin assertion returns
401. Unknown installation or invalid proof returns 404. Stale version, address
conflict and non-ACTIVE project return 409. Invalid input returns 400.
Responses use the existing envelope and Cache-Control: no-store.

The config file has current_key_version, fingerprint_key, encryption_keys
(entries with version and key), and projects. Each project has app_id, issuer,
audience, purpose, namespace, verification_key, origin_issuer, origin_audience,
origin_key, and routes. A route has platform, address_type, provider_namespace
and provider_route_key. Keys are standard base64. The server chooses the route
by project, platform and address type. Each pair has at most one configured
route. Per-project broker verification and provider eligibility require
deployment evidence. A consumed origin assertion must be replaced before
retrying enrollment.

## Consumer integration

Before enrollment, obtain an access JWT for the selected project and an origin
assertion from its configured broker bound to the exact JWT. The subject must
already have an explicit identity mapping in that project's namespace. Submit
the provider address using the configured platform and address type. Persist
the returned installation_id, proof, version and owner_epoch in protected local
storage. Update the stored version and epoch only from a successful response.

Use the same proof and current version for refresh and logout. For an account
switch, use the destination account's project JWT together with the installation
proof and version. Keep historical receipt attribution separate from the new
owner state. Receipt submission is covered by the later receipt milestone.

The backend's local consumer test runs two separate HTTP clients against
PostgreSQL for Android and iOS with the same subject in different projects.
It covers enrollment, cross-project JWT/origin/proof rejection, shared-provider
address collision, logout and an unaffected refresh in the other project.

The first consumer source checkpoint on 2026-11-04 found kaiapps2 on branch
feat/notifications declaring react-native-onesignal and initializing OneSignal
in src/notification/OneSignalService.ts. Its migration still needs secure proof
storage, direct provider address acquisition, project-specific JWT/origin
integration and lifecycle calls. Released-device behavior and broker/platform
verification remain per-project acceptance items.

## Reconcile a lost lifecycle response

POST /{installation_id}/state under the installation collection accepts only
proof and the project mobile Bearer JWT. A valid proof with the current owner
returns installation_id, version, owner_epoch and has_active_binding. After
logout clears ownership, a valid project user holding proof can reconcile the
unbound state. A different current owner returns 404. Proof is never reissued.
Use this state before retrying a mutation whose commit outcome is uncertain.
