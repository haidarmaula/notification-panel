# M06 route-bound FCM transport

Date: 2026-11-05. Status: implemented and locally tested; delivery runtime stays disabled.
Related: [execution](m06-execution.md), [receipt/payload](m06-receipt-payload.md).

## Request and resource boundary

One client pins a Firebase project, environment and explicit application routes.
All routes share logical/physical pacing, concurrency and a circuit breaker.
Separate clients/replicas must receive explicitly partitioned project quota; this
library is not a distributed quota service. No default production rate is implied.
Only declared routes may build a per-device payload; no raw SDK message API exists.

An acquired permit precedes the DB device claim. Failure to acquire consumes no
attempt. On claim failure, release the permit neutrally. Sending/releasing copied
permits shares one-use state. An unused permit cannot start after client shutdown.
The later executor must separately consume its private attempt proof and recheck
DB lease/deadline/current binding immediately before submission.

Logical admission and physical HTTP start each have their own pacing gate at the
configured request rate. This preserves pre-claim rate permits while controlling
actual request timing after DB delays. There is no unbounded internal queue or
background delivery goroutine. Request/concurrency waits honor cancellation. Sending also bounds the credential/HTTP
context by the accepted delivery deadline. Android TTL is recomputed after the
physical pacing wait; iOS retains its absolute APNs expiry. Local timing cannot
guarantee a device display deadline across network/provider uncertainty.

A trusted cloud auth TokenProvider receives the bounded call context. Its
implementation must honor that context and owns credential acquisition/refresh.
Credential file/ADC loading, managed provenance and rotation remain deployment
composition work. No client data selects credentials, project, host or key URL.
Only Bearer access tokens with lifetime covering the call are accepted; tokens
and raw SDK/network errors never enter results/logs. Cloud auth is already pinned
in the dependency graph; using its context-aware interface adds no module/version.

Production HTTP uses verified system roots frozen at construction, TLS 1.2+,
no proxy or redirects, bounded connections/headers/body and the exact HTTPS FCM
project send URL. HTTP/1 only and a non-replayable POST body prevent Go transport
retries outside the SDK guard. Reject inherited root overrides. An unexported test seam rewrites
that exact request to the isolated fixture; deployed configuration has no override.

## Hidden SDK retries

Pinned Admin Go v4.21.0 retries network errors and HTTP 503 internally. Each logical
call carries a private record; its transport permits at most one physical request.
A network failure, response-read failure or completed 503 cancels that child call
before SDK backoff can issue another request. A shared atomic guard is an additional
barrier. No provider response status is rewritten to manipulate retry behavior.

Response reads are bounded to 64 KiB. Known complete error responses and retry
headers remain summarized even when child cancellation ends SDK retry waiting.
Incomplete/error responses remain unconfirmed. Unknown outcomes never refund the
already committed claim and do not imply that the provider failed to accept it.

## Outcomes and retry policy

Return only bounded safe reason codes, disposition, physical request count,
validated provider message name and eligible retry time. Acceptance is not device
delivery. Only typed UNREGISTERED with HTTP 404 permits later conditional
invalidation of the original binding generation. Generic 400/404, sender mismatch,
credential/APNs errors and unknown failures must not purge registrations.

Retryable failures have ordinary 10s or quota 60s floors, honoring valid delta/date
Retry-After without creating a call at/after accepted deadline. Retry times beyond
the deadline are represented by that deadline: terminalization, not another send.
Unrepresentable positive provider delays likewise forbid retry within the window.
Payload/sender/auth reasons remain distinct and require appropriate finalization or
operational holds in the later result protocol.

Breaker outcomes are one per logical device result, never per SDK retry. Accepted
submission resets the closed streak; permanent/local/aborted reservations are
neutral. After configured transient threshold, defer new permits until cooldown,
then allow one probe. Stale in-flight generations cannot close a newer breaker.
Existing admitted calls may finish. Provider quota advice also defers new permits.
The project-wide cooldown is independent of an individual delivery's deadline;
expiring that delivery cannot shorten the shared provider hold. Unrepresentable
positive delays hold until the maximum supported timestamp; operators must
investigate rather than silently retry sooner.

Close prevents new admission, cancels and joins active acquisition/send work, then
closes idle HTTP resources. It does not wait for an unused permit whose caller
has stopped executing; that permit is revoked and cannot send. Credential-source
background resources remain the source owner's responsibility. All callback
implementations must honor cancellation; there are no detached timeout wrappers.

## Verification and remaining integration

Unit/race tests cover permit copies, pacing, cancellation/join, breaker state,
redaction, response bounds, Retry-After and typed errors. The actual SDK passes
against the existing local Postman simulation, including its retryable 503 path,
with assertions that one logical call causes one physical FCM request. Fixtures
use synthetic credentials/addresses. See [verification](../analysis/m06-fcm-transport-verification.md).
The client is process-local; restarts lose its cooldown/breaker state. Durable
per-attempt retry/finalization and deployment restart/backpressure policy remain
part of the later executor and operational acceptance.

Credential loading/startup policy, DB-proof-aware submission, fenced results,
retry/recovery/finalization/update outbox and worker composition remain required.
Numeric SLO/quota allocation, real Firebase/APNs/mobile behavior and release gates
remain unverified. Reference: pinned SDK `internal/http_client.go`, cloud auth
`TokenProvider` contract, and primary
[FCM retry guidance](https://firebase.google.com/docs/cloud-messaging/scale-fcm).
