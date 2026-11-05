# M05 replay command protocol

Date: 2026-11-05. Status: implemented and unit-tested as the dependency-injected
`internal/requeue` adapter. The `cmd/requeue` executable, deployment file/session wiring, source verification and operational issuer integration remain open.
This protocol does not activate a broker or grant production replay authority.
See the [local verification record](../analysis/m05-replay-command-verification.md).

## Input and invocation

`Run` accepts `--execute` or help; the default is an authorized advisory inspection.
It reads one JSON object from stdin, at most 64 KiB, with these exact fields:

```json
{
  "application_id": "00000000-0000-4000-8000-000000000001",
  "request_id": "00000000-0000-4000-8000-000000000002",
  "change_reference": "INC-42",
  "credential": "<short-lived operational assertion supplied through stdin>",
  "positions": [
    {
      "log_source_id": "00000000-0000-4000-8000-000000000003",
      "partition": 0,
      "offset": 7
    }
  ]
}
```

The credential shown above is a placeholder, not a usable assertion. Never put a
credential in argv or retain the input envelope in ordinary incident logs. The
trusted issuer must bind the complete selection to the request fingerprint
specified in the [credential contract](m05-replay-credential.md).

Require UTF-8, canonical nonzero UUIDs, all fields present and non-null, exact
case-sensitive field names, no duplicate or escaped-equivalent duplicate keys,
no unknown fields, and no trailing value. Positions have int32 partition and int64
offset, both nonnegative. Reject duplicate positions and selections outside
1–100. The reference follows `ReplayRequest.Validate`: 1–255 UTF-8 bytes without
edge whitespace or control characters. The assertion is 1–8192 bytes without
whitespace/control characters. Malformed input fails before opening resources.
No `--actor`, role override, credential argument or source-approval flag exists.

## Execution and reports

Process the supplied position order serially, without batch or transaction
retries. The full request remains unchanged for each service call so that the
signed fingerprint cannot be narrowed. A session factory receives validated
input and the requested mode; it must establish trusted configuration and
credential/source verification before opening execution resources.

Inspection delegates to `ReplayRequests.Inspect`. Execution delegates to
`ReplayExecutor.ReplayOne`; it does not implement separate permission or claim
logic. Current DB authority, grant, acceptance, lineage and publication fencing
remain mandatory service checks for each position.

Write one JSON object per processed position to stdout immediately after the
service result. Reports include request UUID, mode, selected physical position,
allowlisted disposition and, only on a successful authorized result, campaign
identity. Never serialize the service result itself: it can hold a private claim
proof. Reports omit credential, reference, raw key, payload and underlying errors.

Logical held/rejected/expired/prepared/terminal/resolved results may continue.
Any authority, input-state, database, infrastructure or uncertain-publication
failure stops processing. An uncertain disposition also stops if a backend
mistakenly omits the corresponding error. Unknown or mode-inappropriate service
outcomes fail closed and are not printed. Cancellation is checked before opening
resources and between positions. Earlier acknowledged reports remain visible.
A report write failure stops all subsequent operations; missing output cannot be
interpreted as proof that no durable work occurred.

Diagnostics go to stderr and use fixed safe messages. Exit codes are 0 for a
completed batch/help, 1 for operational failure, and 2 for invalid arguments or
input. Logical dispositions require operator interpretation even with exit 0;
exit 0 is not a device-delivery acknowledgement.

## Resource ownership and activation gates

The session factory owns cleanup on failed initialization. After successful
initialization the adapter calls `Session.Close` exactly once, before returning,
including after errors or cancellation. A close/drain failure makes the command
fail while retaining any earlier result rows. The command must not detach a
producer drain into an unjoined goroutine. Callers own stdin and must arrange
interruptible reads; a byte limit is not a time limit on an incomplete pipe.

The production factory and executable still need:

- A read-once trusted configuration source and pinned operational verification key.
- Explicit database/TLS and broker TLS/SASL/ACL policies without legacy config or
  ordinary staff JWT fallbacks.
- Verification of the approved source registration, actual broker cluster/topic
  and reviewed incarnation manifest before enabling publication.
- Signal handling, producer drain and source/epoch handoff behavior tested with
  actual PostgreSQL/Kafka fixtures, including initialization and output failures.
- Operational grant issuance/revocation and trusted step-up issuer integration.

The adapter tests exercise command behavior using substituted sessions. They do
not establish real database authorization, source identity, broker I/O drain or
production readiness; those are separate service/integration/deployment gates.

## Static deployment configuration

`LoadConfiguration` now validates a separate trusted JSON stream, at most 64 KiB.
It retains a private read-once verifier, database pool configuration and expected
source manifest without opening a network connection. Every field below is
required; duplicate/unknown/aliased/null fields and trailing values are rejected.

| Field | Meaning |
| --- | --- |
| `database_url` | Explicit PostgreSQL URI with host, port, user, password and effective database name; `sslmode=verify-full` is mandatory. |
| `issuer`, `audience`, `deployment` | Dedicated operational assertion trust scope. |
| `verification_key` | Canonical standard-base64 Ed25519 public key, exactly 32 bytes and not all zero. No private signing key is loaded. |
| `log_source_id` | Canonical nonzero source UUID. |
| `cluster_identity` | Expected broker cluster identity, 1–255 bytes. |
| `topic_name` | Exact Kafka topic, 1–249 ASCII letters/digits/dot/underscore/hyphen, excluding `.` and `..`. |
| `topic_incarnation` | Expected reviewed incarnation label, 1–255 bytes. |
| `partition_count` | Positive int32 expected partition count. |

Cluster/incarnation labels reject edge whitespace and controls. Source values
are expectations, not proof of a live broker or authorization to activate one.
The session factory must compare them with current database registration and
broker metadata, with the reviewed incarnation manifest as a separate gate.

The URI permits only `sslmode`, `sslrootcert`, `sslcert`, `sslkey` and
`connect_timeout` query parameters, each once. Reject keyword DSNs, multiple
hosts, query host/identity overrides, missing effective database names (including
slash-only/encoded-slash paths), and any effective unverified TLS or fallback
connection. Preserve percent-encoded passwords. TLS is pinned to at least 1.2;
the pool has at most two connections and no minimum idle population.

Nonempty `PG*` environment settings fail closed instead of silently supplying
service, certificate, connection or SQL options. Unconfigured certificate/root
paths explicitly override pgx home-directory defaults; absent custom roots use
system roots with hostname verification. Implicit passfile lookup is neutralized.
The loader does not mutate process environment or read `.env`.

The caller still owns trusted-file permission/parent-directory checks and stream
cancellation. Broker TLS/SASL/ACL configuration, live source checks, executable
composition and actual connection/drain tests remain open. Parsing a verified-TLS
policy does not establish that a server certificate, role or network is usable.
