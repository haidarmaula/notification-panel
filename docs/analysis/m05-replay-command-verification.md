# M05 replay command adapter verification

Date: 2026-11-05. Scope: `internal/requeue` and the non-mutating
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

Next: trusted configuration + executable/session composition, source/manifest and
transport verification, then real PostgreSQL/Kafka command and shutdown tests.
Modern worker/scheduler wiring and M06/M07 remain open. No production source,
application or replay command has been activated.


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

Trusted-file opening, broker transport policy, live source/manifest verification
and the executable/session factory are still the next implementation boundary.
