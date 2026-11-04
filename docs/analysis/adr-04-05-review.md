# ADR-04/05 review and revision

Date: 2026-11-02 (Asia/Jakarta). Status: design revision complete, still
proposed; not yet tested or accepted for implementation/production.

Documents: [ADR-04](../adr/adr-04-provider-selection.md),
[ADR-05](../adr/adr-05-receipt-ingestion.md), and related contracts in
[ADR-01](../adr/adr-01-provider-retry.md). Quotations/findings below refer to
the pre-revision version; the table explains their replacements. Runtime code was not changed.

## Findings and design responses

| ID / priority | Pre-revision finding and impact | Design response | Required verification |
| --- | --- | --- | --- |
| P1 / blocker | ADR-04 defaults to OneSignal although the user requested FCM; IOS platform mapped to raw APNS | A4-1/A4-3: explicit FCM, typed address/project/app; IOS FCM does not become APNS; legacy not yet eligible | Startup matrix, typed enrollment, and cohort cutover |
| P2 / blocker | Existing registerDeviceToken retains the old user; refresh/transfer lifecycle does not protect intents | A4-3/A4-4: immutable binding per generation, installation proof/version, owner_epoch, and conditional invalidation | Concurrent refresh/logout/account switch, lost secret, old provider outcome |
| P3 / blocker | Cascade FKs can delete pending deliveries/history; unique raw tokens do not support immutable history | A4-4: tombstone/restrictive deletion, active scoped uniqueness, controlled retention | Migration/backfill/PII lifecycle tests; ADR-10/11 still required |
| P4 / blocker | One user receipt marks all their devices; body JWT/guessed notification does not bind device evidence | A5-1/A5-2: bearer user + signed per-delivery payload capability, original owner, and owner_epoch | Cross-device/foreign-user/forged capability/reassigned owner tests |
| P5 / blocker | PENDING receipt gets a 200 no-op without storage, although callbacks can precede worker results | A5-3 and revised A1-10: store evidence atomically; early receipts may advance PENDING; workers must not regress; late FAILED evidence separate | Both transaction orderings, retry suppression, failure-after-observation |
| P6 / blocker | DELIVERED also marks the user as having read; targeted uses COALESCE(COUNT(...), live users), which never falls back and is not historical | A5-3/A5-4: read only on OPENED; explicit user/device snapshots and metric units | Multi-device stats, empty/incomplete/legacy snapshot, population changes |
| P7 / blocker | Unique(notification,user,status) merges devices; events lack stable ID/version and dedup disappears when outbox is deleted | A5-5: delivery identity/version, stable event ID, durable observation dedup, full projection event | Duplicate after drain, ack-before-delete, downstream version handling |
| P8 / blocker | ORDER BY + SKIP LOCKED batching does not prevent replicas publishing higher versions first | A5-5: one-row bounded publish, per-delivery anti-predecessor predicate, persisted retry, consumer version guard | Two drainers/locked predecessor/backoff/fairness tests |
| P9 / should-fix | SDK construction claimed as full credential validation; existing FCM adapter called production-quality without execution; SendAll deprecated | A4-2/A4-5: bounded init versus remote permission evidence, ADC/file modes, supported bounded submission method | IAM/APNs preflight, SDK retry/concurrency audit, actual-device test |
| P10 / should-fix | Old documents state FCM has no delivery telemetry at all | A5-1: client observations chosen for v1; optional reporting/export acknowledged with coverage/latency limits | Mobile reporting coverage; optional export evaluated if needed |

## Repository and dependency evidence

- `internal/features/mobile/service.go`, `registerDeviceToken`: IOS becomes
  APNS; updates use UpdateDeviceTokenFull without changing user_id.
- `internal/features/mobile/routes.go` and `cmd/server/main.go`: /mobile/sync
  uses a body JWT; the module has not yet wired bearer auth for receipts.
- `internal/middleware/mobile_jwt.go`, `MobileJWTMiddleware.Use`: bearer HS256
  and external_id are already available for device-token routes; expected issuer/
  audience and required expiration are not yet enforced. The auth server/mobile issuer
  must be agreed, rather than assuming safety from the middleware name.
- `db/migrations/000002_create_device_tokens.up.sql`: global unique push_token
  and cascading user FK. `000012_create_notification_deliveries.up.sql` has
  user/device cascades. `000013_create_notification_reads.up.sql` has
  unique(notification_id,user_id), suitable for a user projection, not a device fact.
- `internal/kafka/events.go`, DeliveryUpdated: notification/user/provider/status
  without event ID, delivery ID, or version. ProviderMessageID is not a substitute
  for delivery identity stable across all attempts/providers.
- `go.mod`: Firebase Admin Go v4.21.0. Dependency source
  `messaging/messaging_batch.go`: SendAll deprecated, SendEach <=500 and
  internal numWorkers=50. `messaging/messaging.go`: Message.Fid available,
  Message.Token deprecated. Do not assume an unread mobile SDK
  supports modern identifiers merely because the server SDK supports them.

## Additional decisions and scope limits

1. The proposed FCM path does not require OneSignal credentials or a backend APNs
   client. The Firebase project manages APNs integration; operator/project
   owner access still needs to be determined. There is no automatic cross-provider fallback.
2. Modern registration uses the existing /device-tokens bearer surface with
   explicit extensions; old /mobile/sync remains legacy during migration. All
   entry points must use the same service invariants to prevent bypasses.
3. owner_epoch changes on logout/account switch, separately from address version.
   An address refresh for the same user can still report previous pushes;
   switching accounts and back does not revive old receipt epochs.
4. A signed receipt capability proves payload possession within the client/user
   trust model; it is not hardware attestation. A 7-day lifetime, key rotation, and
   enrollment proof remain proposals for security/mobile review.
5. OPENED/DELIVERED may precede confirmed provider acceptance. Store
   separate facts; do not invent provider_message_id/sent_at. FAILED with
   late observations remains terminal for retry and is displayed as a
   discrepancy, rather than automatically revived.
6. Event schema v2 and new stats need compatibility review with consumers
   and the frontend repository. This invalidates the old claim that wire payloads are unchanged.

## Integration questions and release gates

| Decision | Owner/facts required | Progress boundary |
| --- | --- | --- |
| Firebase project/app/environment | Firebase/ops owner; Android package/iOS bundle and APNs environment | Before credential configuration and pilot |
| Mobile framework and SDK versions | Mobile repository/team; FID enrollment versus token compatibility | Before registration contract/SPEC acceptance |
| Laravel issuer/audience/expiry/logout | Auth and mobile owners | Before publishing auth/receipt endpoints |
| Installation credential/recovery and app attestation | Mobile + security; storage/rotation/ownership proof | Before production enrollment |
| Retention, PII deletion, receipt window | Product/ops/security, ADR-10/11 | Before schema/SPEC acceptance |
| DeliveryUpdated consumers | Analytics/integration inventory outside the repository | Before enabling event v2 |
| Final SLO and rate budget | Benchmark/quota/ops | Before tuning and go/no-go |

No raw secrets/private keys are requested. These questions do not block writing
ADR-06–11, but must not be replaced with hidden assumptions when implementation starts.

## Method and verification

The review read source, SQL, middleware, ADRs, and local
dependencies. Orbit discovery was attempted but failed DNS resolution; fallback used
primary Firebase documentation and the pinned SDK. No FCM request, live
credential initialization, `.env` read, device test, Postman workspace push, migration,
runtime test, or benchmark occurred in this documentation work.

Primary sources checked on 2026-11-02:
[Admin setup](https://firebase.google.com/docs/admin/setup),
[Apple setup](https://firebase.google.com/docs/cloud-messaging/ios/get-started),
[send methods](https://firebase.google.com/docs/cloud-messaging/send/admin-sdk),
[registration management](https://firebase.google.com/docs/cloud-messaging/manage-tokens),
[delivery telemetry](https://firebase.google.com/docs/cloud-messaging/understand-delivery).

This review completes steps 9–10 at the design level. Implementation and
verification remain open. Next is step 11: ADR-06 OneSignal/mobile migration,
written against the revised typed enrollment and receipt contracts, without
promising unproven identifier conversion or zero-impact rollback.
