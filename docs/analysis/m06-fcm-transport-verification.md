# M06 FCM transport verification

Date: 2026-11-05. Scope: `internal/fcm`, a route-bound single-device SDK transport.
Contract: [FCM transport](../specs/m06-fcm-transport.md). The worker remains
admission-only; this package does not grant database execution authority.

## Local evidence

Unit/race tests cover copied permit one-use, bounded concurrent HTTP, cancellation
and joined credential/HTTP callbacks, shutdown revocation, separate physical pacing
after claim delays, breaker generation/probe handling, safe formatting, invalid
credentials/routes/configuration, inherited TLS root rejection, response-size
bounds, typed FCM errors and retry floors. Android TTL is shortened after local
pacing while other payload fields survive; expired/malformed/oversized SDK bodies
fail closed. No detached timeout goroutine wraps an uncooperative callback.

Focused independent review found FT1: the per-delivery retry deadline could
shorten the shared project quota hold. Shared cooldown now retains provider advice
independently, and a short-deadline regression verifies both values. See
[review record](../../code-review.md#m06-fcm-transport-review--2026-11-05).

The actual Firebase Admin Go v4.21.0 SDK passes through the new guard and a private,
exact-target loopback rewrite to the existing Postman simulation. Four platform/
address combinations (Android/iOS, FID/registration token) are accepted. Typed
unregistered, quota, 503 and timeout return the expected normalized outcomes.
Every case asserts one base HTTP request. The 503 path returns without SDK retry
backoff; timeout remains unconfirmed rather than claiming non-acceptance.

Focused runner evidence: `hello/tests/integration` passes in 8.995 seconds and
`hello/internal/fcm` in 2.635 seconds, with the race detector. Simulation records
16 completed SDK requests: 200 × 6, 404 × 2, 429 × 2 and 503 × 6. These totals include
the unchanged dependency test, which still issues five 503 requests without the
guard, versus one from the guarded transport. Delayed/cancelled responses are not
counted as completed status events. All 29 migrations pass up/down/up in the
isolated environment; fixture containers/volumes are removed afterwards.

The complete isolated suite also passes with race detection:
`hello/tests/integration` in 100.445 seconds and `hello/internal/fcm` in 2.554 seconds.
Its simulation records 21 completed requests (200 × 11, 404 × 2, 429 × 2, 503 × 6),
including the prior scoped-payload tests. Full unit/race, build, vet (including
integration code), formatting and module verification pass. The current
vulnerability scan finds zero affected call paths; one imported-package and three
uncalled-module advisories remain. This is not a vulnerability-free dependency graph.

Cloud auth was already pinned transitively at v0.20.0. It is now a direct import
for context-aware `TokenProvider`; no module/version or checksum is added.

## Remaining acceptance

This is local fixture evidence, not real OAuth, Firebase project permission,
APNs configuration, device delivery or capacity/SLO evidence. Production HTTP uses
verified TLS/system roots, HTTP/1 and a non-replayable POST; the fixture uses plain
loopback HTTP through an unexported seam. The seam is unavailable in deployed
configuration. Physical count means base RoundTrip invocations, not proof that
FCM saw or accepted a request.

The caller owns a trusted cancellation-aware token source. Protected credential
loading/rotation, explicit quota allocation across replicas, DB-proof-aware
single-use submission, fenced result persistence/conditional binding invalidation,
retry/recovery/finalization/update outbox and delivery worker composition remain.
Process restart loses local cooldown/breaker state; deployment restart policy and
durable per-attempt retry policy still need acceptance. Unknown network outcomes
can produce duplicate delivery on a later attempt; receipt/app deduplication and
the M06/M07 shared acceptance gate remain required.
