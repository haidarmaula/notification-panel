# M06 file credential verification

Date: 2026-11-05. Scope: `fcm.NewFile`, protected secret reading, synchronous cloud
auth 2LO refresh and owned HTTP cleanup. Contract: [file credentials](../specs/m06-fcm-credentials.md).
This does not enable delivery or complete ADR-04's managed ADC/deployment gates.

## Local evidence

Unit/race tests cover exact service-account project/email, explicit cross-project
approval, required/optional JSON fields, duplicate/null/unknown/case/trailing
rejection, fixed token URI/universe, valid PKCS8 and PKCS1 RSA, weak/prefixed/
multiple-key rejection and private file-path denial. The 2LO library's generated
assertion is verified with the synthetic public RSA key; issuer, audience, scope,
expiry, grant type and absence of delegation are asserted. No handwritten JWT
signer replaces the Google library.

Concurrent refresh callers share one bounded exchange; returned token mutation
cannot alter the cache. Near-expiry refresh is synchronous. Gate waiters cancel
independently, active HTTP observes cancellation and joins, invalid/oversized/
overflow-expiry/ID-token responses fail safely, and rejected credentials cannot
reach FCM. Raw provider responses and SDK errors are not returned; the library's
logger is explicitly discarded, including with its debug environment enabled.
Focused security review found no additional concrete defect.

The actual cloud auth and Firebase SDK run through the existing local Postman
simulation. Success uses one OAuth request for two accepted FCM sends. Invalid
grant and a 100 ms caller timeout make no FCM request. Focused race run passes:
`hello/tests/integration` in 9.916 seconds, `hello/internal/fcm` in 2.213 seconds;
logs contain the expected OAuth and FCM requests. The fixture validates claim/HTTP
shape; its Google key-registry/IAM validation is deliberately not modeled.
Owner mock discovery was skipped; the local fixture is based on the primary
Google OAuth contract and pinned cloud auth v0.20.0 implementation.

A Linux/arm64 static tagged test binary runs in the existing pinned disposable
PostgreSQL image, with network disabled and only the test binary mounted read-only.
It generates a synthetic key inside the container, accepts a root-owned private
file through the public constructor, then rejects group-write and symlink variants.
No host deployment/secret path is touched. Docker tmpfs ACL inspection returned
`operation not supported` and was correctly refused; the ACL-capable container
filesystem passed. Deployment must verify its actual mount supports the policy.

## Remaining acceptance

The first complete runner attempt passed the Go tests but failed after them
because the executing script was edited during the run. It is not a successful
runner result; the finalized script must pass unchanged through evidence checks
and cleanup. The finalized unchanged runner then passes with race detection:
`hello/tests/integration` in 102.545 seconds and `hello/internal/fcm` in 3.221 seconds,
including 29 migration up/down/up checks and container/volume cleanup. Simulation
logs report 23 completed FCM requests (200 × 13, 404 × 2, 429 × 2, 503 × 6) and
three completed OAuth requests. This includes earlier SDK/transport/payload tests.

Full unit/race, build, native/integration vet, Linux tagged vet, formatting and
module verification pass. The current vulnerability scan reports zero affected
call paths; one imported-package and three uncalled-module advisories remain.
No module/version or checksum changes are introduced.

Real credentials were neither read nor sent. OAuth authentication validity,
target-project IAM (including cross-project grants), quota, APNs and mobile
behavior remain separate authorized preflight gates. Only file mode is composed
here; managed ADC provenance/factory remains open. Worker policy still needs
explicit credential-mode/source selection, role-only mounts, rotation/drain and
reviewed quotas before delivery can be enabled. DB-proof-aware submission,
fenced results/retry/recovery/update outbox and M06/M07 acceptance remain open.
