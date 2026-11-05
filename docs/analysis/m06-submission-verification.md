# M06 submission verification

Date: 2026-11-05. Scope: acknowledged reservation → protected payload → final
DB authorization → one-device FCM. [Contract](../specs/m06-submission.md).
The worker remains admission-only. Results are not yet persisted or published.

## Implemented

Submitter checks the route and obtains a client permit before charging. Reservations
share one atomic consumption bit across copies. It reads original binding ciphertext,
committed content and frozen receipt metadata under the established lock order;
then releases the transaction before decrypting/signing or obtaining credentials.
The transport repeats authorization after credential refresh and physical pacing.
It compares the prepared snapshot and bounds HTTP by a conservative monotonic
cutoff derived from DB remaining lease/deadline, subtracting transaction latency.
Only acknowledged commits permit I/O; there is no blind retry around network calls.

Query-only changes use existing schema; `make sqlc` regenerates the query/interface
code. No new columns or migrations are required. JSON data must contain strings;
null/structured values are rejected. Reads bound payload/title/body sizes in SQL.
Lease/reservation formatting redacts pointers and copied values. Only the tagged
integration build exposes a fixed loopback synthetic-client factory; normal builds
have no endpoint override.

## Verification

Unit/race tests cover string-data rejection, proof redaction, final authorization
after credentials, foreign-client permits and cutoff cancellation. Focused review
found no additional concrete lock-order, commit-proof, ownership or lifecycle defect.

PostgreSQL + actual SDK/Postman tests exercise concurrent copied reservations,
the pre-claim driver, application/policy holds, lineage changes, lease expiry,
resolved attempts, early observations, disabled users, retired bindings, logout
owner epochs and payload changes during credential refresh. A separate connection
obtains the mutation locks during refresh; final checks then block FCM without
refunding the charge. Lost acknowledgement after actual prepare/final-validation
commits prevents sending and does not restore the proof. Missing retained signing
or decryption keys fail before credential/provider I/O.

Focused PostgreSQL/SDK/race scenarios pass in 11.805 seconds. The complete
isolated suite also passes with race detection: `hello/tests/integration` in
103.228 seconds and `hello/internal/fcm` in 2.549 seconds, including retained-key
failure cases and all 29 migration up/down/up checks. Simulation logs contain
25 completed FCM requests (200 × 15, 404 × 2, 429 × 2, 503 × 6) and three OAuth
requests, including existing dependency/payload/credential scenarios. Cleanup
removes the disposable containers/volumes. Full unit/race, build, integration vet,
format and module checks pass. Vulnerability scanning finds zero affected call
paths; one imported-package and three uncalled-module advisories remain.

## Remaining work

Fenced result persistence, conditional invalidation, retry/recovery/finalization,
versioned delivery update outbox and delivery worker composition remain. M07 receipt
ownership/dedup/statistics acceptance is still required. No production credentials
or devices are used; these tests do not establish real IAM/APNs delivery, capacity,
clock synchronization or production activation. Arbitrary suspension and ownership
changes after the last DB check cannot be fully fenced at an external provider.
