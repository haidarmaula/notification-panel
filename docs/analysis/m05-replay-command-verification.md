# M05 replay command adapter verification

Date: 2026-11-05. Scope: `internal/requeue`, `cmd/requeue`, authenticated scoped
Kafka publication, isolated TLS fixtures and the non-mutating
`ReplayRequest.Validate` entry point. Contract:
[replay command protocol](../specs/m05-replay-command.md).

## Implemented behavior

- One bounded, exact-schema stdin envelope; canonical UUIDs, complete fields,
  explicit nonnegative positions, duplicate detection, 1–100 selection bound and
  existing domain reference validation. Credentials cannot be supplied as flags.
- Default inspection, explicit execute, unchanged complete selection on each
  serial service call, immediate safe JSON-line reports and separate diagnostics.
- No retry after a service failure or uncertain publication; stop on unconfirmed,
  abandoned or started outcomes even if a backend omits its uncertainty error.
  Logical dispositions may continue. Earlier acknowledged progress remains visible.
- Mode-inappropriate/unknown results and unknown errors are sanitized; failed
  operations expose no unverified campaign identity, raw evidence or claim proof.
- Cancellation between positions, immediate stop after report write failure,
  exactly-once session close and failure status after close/drain failure.

## Local evidence

Behavior tests were written before the adapter implementation; the initial run
failed compilation because the command/session/report contracts did not exist.
Focused tests then pass with the race detector. They cover malformed/ambiguous
input before resource creation, escaped duplicates, field aliases, null/missing
fields, integer overflow, invalid UTF-8, duplicates, size limits, credential
redaction, default dry-run, logical outcomes, uncertain/error stops, partial
progress, mode/result validation, input-order preservation, cancellation,
initialization/output failures and shutdown failure.

Full unit/race tests, build, vet, module verification, formatting and the offline
31-skill integrity check pass. The required reachable-vulnerability scan reports
zero affected vulnerabilities; one imported-package and three module findings
remain on paths the scanner does not identify as called. No dependencies or SQL
contracts changed.

An independent read-only review found no concrete defect. Its suggested test for
non-normalized position order was added and passes. The review itself did not run
tests; the implementing session ran the checks above.

## Evidence boundary and next work

These are substituted-session command tests. The adapter does not create a real
database connection, verify broker metadata, drain a real producer or provide an
operational assertion issuer. Existing PostgreSQL/Kafka service evidence is
unchanged; this slice does not rerun or expand that evidence.

The executable follow-up below now covers configuration/session composition
and real PostgreSQL/Kafka command tests. Modern worker/scheduler wiring and
M06/M07 remain open. No production source or application has been activated.


## Static configuration follow-up

The private configuration loader now validates pinned operational trust, explicit
verified-TLS database policy and expected source identity. Unit/race cases cover
missing/ambiguous/unknown fields, unsafe or overridden DB transport policy,
missing/zero source data, malformed keys, ambient `PG*` settings and encoded
password preservation. No connection or source activation occurs.

Independent review found that slash-only URI paths could inherit pgx database
defaults, and that pgx otherwise merges environment/service/home certificate
policy. New red regressions reproduced the path and environment defects. The
loader now compares the effective database name, rejects nonempty `PG*` settings,
overrides unconfigured certificate paths and disables implicit passfile lookup.
Focused race tests pass; follow-up review found no remaining concrete gap in
these fixes. See RC1/RC2 in [the code review](../../code-review.md).

The following executable slice implements trusted-file opening, broker transport
policy, registered/live source comparisons and the session factory. Independent
production incarnation approval is still required.


## Executable and authenticated transport follow-up

`cmd/requeue` now loads only the protected deployment path. Ownership, POSIX
permissions, symlink and extended ACL checks cover config/ancestors and DB TLS
files. No argv/environment key or config override exists. Nonempty TLS-root
environment overrides are rejected before parsing and again at session startup.

Startup authenticates before network work, checks runtime/source identity under
canonical locks, performs current DB-authorized inspection, then checks actual
broker cluster/topic/partition metadata through the authenticated publication
transport. Each publication repeats the metadata comparison. No startup result
replaces per-operation authority/claim checks. Dry-run sessions have no executor.
Source incarnation is compared with the immutable DB registration but still
requires an independently reviewed manifest; SDK metadata does not prove its UUID.

Production transport requires verified TLS 1.2+ and mTLS or SASL/PLAIN inside TLS.
The existing plaintext producer remains for isolated fixtures. No dependencies,
SQL migrations or generated files changed. The session drains its producer before
closing its pool; failed initialization closes all acquired resources. Command
cancellation interrupts incomplete input and joins its stream-close callback.
SIGPIPE is ignored so a broken report returns through cleanup.

### Final local evidence

- Focused and full unit/race, build, vet, format, unchanged module verification
  and reachable-vulnerability scanning pass. The scanner still reports zero
  affected call paths, with one imported-package and three uncalled module
  findings; these are not claimed remediated.
- Full isolated PostgreSQL/Kafka suite passes in 88.493 seconds with all 27
  migration up/down/up checks. The global test timeout was raised from 90 to 120
  seconds to accommodate added TLS cases; individual operation/lease deadlines
  were preserved. TLS materials are newly generated, synthetic and short-lived.
- The actual command adapter + session connects through PostgreSQL verified TLS
  and Kafka mTLS. Wrong DB/broker CA, mismatched incarnation and wrong partition
  count fail before request registration. Default inspection writes no header.
  Execute publishes an actual strictly decodable event; repeating the request
  leaves one attempt and a Kafka end offset of one. Session DB connections are
  gone before return. The fixture uses an isolated database role/schema; it does
  not establish production least-privilege grants or Kafka HA.
- Native Darwin ACL/write-security and real-pipe cancellation regressions pass
  with the race detector. Linux ACL regression passes in a disposable,
  network-disabled container using the cross-compiled test binary (without race
  instrumentation). A separate actual Linux executable smoke accepts a protected
  root-owned fixture policy and returns exactly `credential_rejected` for a
  synthetic invalid assertion, with empty stdout and networking disabled.
- An ACL-unsupported tmpfs fixture policy was rejected as designed. The positive
  protected-file smoke uses the ACL-capable container overlay. Production policy
  storage must support inspection; permissive fallbacks were not introduced.
- Existing Firebase SDK simulation observes statuses 200/404/429/503. No real
  provider, mobile device, production credential, MFA issuer or source is used.

Independent review identified Darwin ACL and implicit CA-environment gaps; both
were fixed with regression coverage and rereview. No further concrete defect was
reported. RC3–RC5 are recorded in [the code review](../../code-review.md).

### Next boundary

Wire modern scheduler/outbox/QUEUED recovery/replay cleanup and Kafka admission
under explicit transport, supervision and shutdown ownership, then implement M06.
Real operational issuer/grant provisioning, broker/DB ACLs, source-incarnation
approval, canceled-write/backpressure drain and handoff/restore exercises remain
release gates. SASL/PLAIN configuration is unit-tested; only mTLS has a positive
real-broker authentication fixture. The production plan remains incomplete.
