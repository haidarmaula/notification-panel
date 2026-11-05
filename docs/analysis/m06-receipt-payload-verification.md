# M06 receipt capability and payload verification

Date: 2026-11-05. Scope: pure signer/verifier and scoped FCM payload libraries.
Contract: [receipt/payload v1](../specs/m06-receipt-payload.md). No delivery or
receipt endpoint is enabled, and no real Firebase/APNs credentials are used.

## Implemented boundary

`internal/receipt` uses dedicated Ed25519 keys, fixed version/domain/environment,
canonical bounded encoding and exact persisted-scope verification. Private and
public rings are separately copied and bounded. The explicit key ID and complete
scope make retries deterministic; there is no automatic key/expiry refresh.
Capability values and signer pointer/copied values redact normal formatting,
slog and JSON. Only explicit transport access reveals the capability string.

`internal/pushpayload` pins app/environment/provider route, checks destination
binding/installation/owner metadata against signed scope, sets exactly one FID or
registration-token target, and produces schema-v1 data. TTL/APNs expiration never
extend the accepted deadline. Encoded payload overhead counts toward a conservative
4096-byte budget; user data cannot overwrite contract/FCM-reserved fields. Inputs
are bounded and copied. The returned SDK message remains sensitive transport data.

`internal/cryptokey` screens canonical-y/small-order public-key encodings without
new dependencies or custom curve/signature arithmetic. Receipt, replay assertion,
mobile JWT and enrollment-origin constructors all apply it. This is weak-key
screening, not complete point/provenance validation; standard-library signature
verification remains required. An identity-key forgery regression reproduces the
misconfiguration hazard and proves constructor rejection.

## Local evidence

- Unit/race tests cover deterministic retry, wire field order/domain, every scope
  field, foreign environment, unknown/removed keys, mutation isolation, exact
  expiry, malformed/oversized/padded/newline/noncanonical encodings and redaction.
- Key-screening tests reject both signs of every low-order y coordinate, all
  noncanonical y values and wrong lengths while preserving generated keys/bytes.
  Consumer-constructor regressions cover receipt/replay/mobile/origin trust.
- Payload tests cover Android/iOS x FID/registration-token, same-route identity
  mismatch, reserved data, UTF-8, encoded overhead, deadline/final-subsecond
  rounding, shortening retries, zero values and output/input map independence.
- Five-second parser/signature fuzzing passes 577,662 executions with two workers.
  This is a bounded fuzz run, not exhaustive cryptographic assurance.
- The actual pinned Firebase SDK submits all four builder-produced platform/
  address combinations through the local Postman fixture. The fixture checks
  scope fields, capability version and serialized platform expiration; returned
  capability data also verifies against the original scope. Existing SDK error/
  timeout scenarios remain in the same focused run (8.988 seconds, race enabled).
- Narrow independent review found RP1 (weak configured key), RP2 (copied signer
  redaction) and RP3 (destination identity pairing). Fixes and regressions were
  reviewed again; no additional concrete regression was found. See
  [the review inventory](../../code-review.md).

- Complete isolated integration passes with the race detector in 101.720 seconds,
  including all 28 migration up/down/up checks, existing daemon/replay/mobile/
  origin behavior and the scoped SDK serialization cases. Unit/race, build and
  vet (including integration code) pass. Module/version files are unchanged.
- Reachable-vulnerability scanning reports zero affected call paths; one imported
  package and three uncalled module advisories remain informational. This does
  not establish production readiness or an advisory-free dependency graph.

## Remaining prerequisites

Capability version/key ID/environment/expiry now freeze with the first device
charge in the [metadata follow-up](m06-receipt-metadata-verification.md). The proposed observation window needs approval; it has no production
default here. Implement private-key distribution/rotation, route-bound real
transport, physical-request rate accounting, single-use submission, fenced result
writes/conditional invalidation, recovery/finalization, versioned update outbox and
worker composition. M07 still needs current mobile authority, receipt evidence,
semantic dedup and statistics. Real mobile SDK/project/APNs/device validation,
capacity/HA/recovery and production acceptance remain release gates.
