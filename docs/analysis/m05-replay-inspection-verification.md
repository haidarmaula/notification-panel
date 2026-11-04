# M05 replay eligibility inspection verification

Date: 2026-11-04. Scope: authorized dry-run inspection, shared authority/acceptance
helpers and two read-only SQLC queries. This follows
[request registration](m05-replay-registration-verification.md). It does not
claim positions, resolve evidence or publish messages.

## Implemented boundary

`ReplayRequests.Inspect` requires a selected position in the signed complete
request, current DB send-role permission, explicit app membership and a usable
operational grant. It uses the bounded canonical authority locks, checks any
existing immutable request header, then decodes the stored strict envelope.
Missing/out-of-scope evidence returns the same denial; malformed or mismatched
acceptance reveals no inferred campaign identity. Runtime epochs and every
acceptance field must match exactly.

Admission and inspection share `acceptedSendMatches` without changing admission
semantics. Inspection validates complete-marker epochs/status before reporting
an admission no-op. It distinguishes eligible QUEUED, prepared/terminal,
expired, held, rejected and reconciliation-required work. Active claims remain
held until their owner or stale-claim cleanup finishes; prior resolution cannot
be reopened by inspection. A result is advisory and must be revalidated by the
future claim/publication transactions.

The method writes no header, attempt, audit, control, intent or campaign state.
Its transaction locks are reads with locking, not a PostgreSQL READ ONLY
transaction. Expiry is reported without terminalizing; canonical QUEUED recovery
owns that mutation. Target checks inspect supported metadata, not live audience
snapshots or provider eligibility. Coverage remains a Preparer responsibility.

## Held application correction

Independent review found the initial shared authority guard used the ACTIVE-app
send precondition before checking existing prepared/terminal state. Three HELD
application fixtures proved this wrong. Operational bookkeeping now requires
current DB app membership plus current DB send-role authority, separately from
app eligibility. Prepared/terminal/expired classification precedes the app hold;
QUEUED work on a non-ACTIVE app remains held. A registration on a held app only
records metadata and audit, never an attempt/claim/intent. Normal send permission
policy is not changed.

## Evidence

PostgreSQL fixtures cover eligible/held/retry-due/unsupported metadata,
terminal and empty prepared snapshots, inconsistent QUEUED markers and mismatched
marker epochs, HELD app no-ops, cross-app/missing/malformed/stale evidence,
acceptance mismatch and expiry. They assert unchanged campaign status and zero
dry-run writes. Additional controls fixtures check that busy/resolved source
positions remain byte-for-byte unchanged.

Full unit/race, build, vet (including integration-tagged code), format and the
full PostgreSQL/Kafka integration runner pass after the shared matcher/authority
changes. All 26 migrations pass up/down/up. Later malformed/stale/control-state
fixtures pass in a focused runner with the existing Firebase SDK HTTP simulation.
No CLI, production issuer, actual broker-manifest verification, provider send or
real device is exercised.

## Next slice

[Claim transactions](m05-replay-claim-verification.md) now implement per-position
attempt/claim/audit while keeping the dry-run mode free of writes. Next implement
publication-time
revalidation, bounded synchronous broker I/O and atomic result/resolution/audit,
then service-owned stale-claim cleanup. Preserve the defined lock order even when
creating an absent control row; dry-run locking is not a claim protocol. The CLI
remains disabled until execution and failure-window evidence exist.
