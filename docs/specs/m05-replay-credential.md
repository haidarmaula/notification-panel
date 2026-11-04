# M05 operational replay credential

Status: local provenance-verification library, 2026-11-04. This implements input
binding and cryptographic verification for the [reprocessing protocol](m05-operator-reprocessing.md).
It does not implement database authority/revocation, audit, publication or a CLI.
A valid assertion alone is insufficient to mutate replay state.

## Trust and lifetime

`ReplayVerifier` pins an Ed25519 public key, exact issuer, single audience and
deployment identifier. Construction copies the key; JWT headers cannot select
remote keys or another algorithm. Provision a dedicated operational trust policy;
there is no staff/mobile access-token fallback. The trusted issuer must actually
verify MFA before asserting `amr: ["mfa"]`; signature verification does not perform
MFA itself. Issuer/step-up integration remains a deployment gate.

Assertions have at most five minutes between `iat` and `exp`. Require a positive
canonical decimal staff subject, nonempty `jti`, explicit issuance/expiry,
`auth_time` at or before issuance and no more than five minutes old, and MFA in
`amr`. Future issuance/not-before, expired credentials and credentials with less
than five seconds remaining are rejected without clock leeway. Deployment clocks
must be maintained. Recheck lifetime before actual publication.

| Claim | Binding |
| --- | --- |
| `purpose` | Exactly `notification_replay` |
| `deployment` | Exact configured deployment |
| `iss`, `aud` | Exact pinned issuer and one audience |
| `sub` | Staff identity; current database role/status still required |
| `jti` | Credential identifier; not a replacement for durable request idempotency |
| `application_id`, `request_id`, `grant_id` | Nonzero canonical UUIDs |
| `selection_hash` | Unpadded base64url SHA-256 request fingerprint |
| `iat`, `exp`, `auth_time`, `amr` | Bounded issuance, lifetime and trusted MFA evidence |

Tokens are bounded to 8 KiB. Verification failures expose fixed errors rather
than raw tokens, claims or decoder messages. Successful verification copies and
normalizes the request; caller mutation cannot change verified selection.

## Stable request fingerprint

Validate 1–100 unique positions, nonnegative partition/offset, nonzero UUIDs and a
1–255-byte UTF-8 change reference without edge whitespace or control characters.
Sort a copy by source UUID bytes, partition, then offset. Do not trim/repair input
or reorder caller memory. The issuer and backend must use the same v1 encoding:

1. Literal bytes `hello:notification-replay-request:v1` followed by one zero byte.
2. Application UUID and request UUID, each their 16 raw bytes.
3. Positive staff ID as an unsigned big-endian 64-bit integer; grant UUID bytes.
4. Reference UTF-8 byte count as a big-endian 16-bit integer, then reference bytes.
5. Position count as a big-endian 16-bit integer; for each sorted position append
   source UUID bytes, partition as unsigned big-endian 32-bit, offset as unsigned
   big-endian 64-bit. Validation excludes negative signed inputs before conversion.
6. SHA-256 of those bytes, encoded as strict unpadded base64url in the assertion.

Changing actor/grant, application/request, any selected position or reference
changes the digest. Reordering a selection does not. A fixed test vector protects
this persisted v1 contract; changing the encoding needs explicit versioning.

## Verification and remaining gates

Unit/race tests cover valid and reordered selections, mutation isolation, changed
scope/actor/grant/selection/reference, missing and invalid claims, stale/future MFA,
issuer/audience/deployment separation, wrong signing key/algorithm, missing
configuration and copied key material. Independent review found no concrete defect
within this provenance-only boundary and prompted additional negative cases.

Before using `VerifiedReplay` in a transaction, lock and recheck current staff,
explicit app grant, separately revocable operational grant, grant selection and
expiry, source/deployment controls and exact campaign acceptance. Keep these
checks through the bounded publication/result commit as specified by M05. No
request/claim mutation is exposed by this library.
