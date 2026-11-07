# Expiring mobile request markers

Migration 000032 and the one-shot `retention` command implement two application-scoped operations. Both require a reviewed policy with an explicit post-expiry retention period in seconds. No lifetime, grant, issuer or schedule is seeded.

| Class | Eligible rows | Preserved records |
| --- | --- | --- |
| `origin_nonce` | Signed enrollment assertions whose stored expiration is at or before the grant cutoff | Live nonces, installations, binding generations, proofs and user mappings |
| `receipt_request` | Client event IDs whose recorded expiry exactly matches the frozen delivery capability expiry, with both at or before the cutoff | Unexpired request IDs, signing metadata, semantic observations, reads, submission attempts, outbox and campaign detail |

Receipt verification uses the database clock and rechecks session and installation ownership. An expired capability cannot recreate a delivery or submit a new fact. Cleanup permits reuse of an old client event ID for a different valid request after the approved window. It does not extend the old report window. The signed-origin consumer checks the database clock after acquiring its application lock. A batch takes a conflicting application lock so even a request already waiting when cleanup starts must satisfy the expiry check. Before granting maintenance access, retire binaries that run the older unlocked nonce query.

## Provisioning and holds

Use the fixed `public` application schema in a protected database. Revoke CREATE on that schema from untrusted roles. Migrate transactionally as the reviewed DB owner. The security-definer function fixes its search path and qualifies the application tables. The migration revokes the default PUBLIC EXECUTE privilege. For deployment, transfer the function to a non-login owner with only these permissions:

- SELECT on the runtime row, applications, the two marker tables, frozen receipt metadata, policies, grants and batch evidence.
- UPDATE privilege on at least one column of each of the runtime, application, policy and grant tables for their row-locking SELECT operations. Do not make the operator a member of this owner role.
- DELETE on the two marker tables. INSERT on batch evidence and UPDATE on its applied time, outcome and removed count.

Separate the maintenance login, policy/hold administrator, grant issuer and audit reader. The maintenance login needs CONNECT, schema USAGE and EXECUTE on `public.run_retention_batch(uuid,uuid,uuid,uuid,text,integer,boolean)` only. Deny table writes, grant issuance, role membership and privileged role attributes. Pin its PostgreSQL statement, lock and idle-in-transaction limits in the role settings. Test the effective grants and negative permissions before activation. PostgreSQL documents the [security-definer privilege boundary](https://www.postgresql.org/docs/17/sql-createfunction.html) and [row lock conflicts](https://www.postgresql.org/docs/17/explicit-locking.html).

The policy authority creates one `retention_policies` row per application and class with a new policy-version UUID and the approved additional retention in seconds. The hold defaults to true. Review legal, privacy, recovery and incident holds before setting it false. If a narrower hold exists anywhere in the application and class, hold the entire scope. An absent control or a held scope denies both preview and apply. Hold changes serialize with in-flight batches. A hold acknowledged by the database applies to subsequent work. Revoke the old grants and issue a new policy version when eligibility changes.

The separate grant issuer inserts an immutable `retention_grants` scope with a fresh UUID, current policy version, operator login OID, both current runtime epochs, a finite cutoff/expiry, one batch size (1–500), maximum batches (1–1000) and whether apply is authorized. Only revocation is mutable. Resolve the operator OID from the approved login directly on the target cluster. Preserve the external approval, policy owner/purpose, legal-hold review, ticket and authenticated issuer/operator identities under the corresponding grant and policy UUIDs. A copied or restored database needs independent reconciliation and authorization before the login can connect.

## Running one batch

Mount the protected role file at `/etc/hello/retention.json`. Supply exactly the fields shown, using the approved identifiers, class and verified TLS credentials. Provide a dedicated private database network. No Kafka, provider credentials or mobile keys are required.

```json
{
  "database_url": "postgres://retention:ENCODED_PASSWORD@db.example.internal:5432/notifications?sslmode=verify-full",
  "grant_id": "GRANT_UUID",
  "application_id": "APPLICATION_PUBLIC_UUID",
  "origin_epoch": "ORIGIN_UUID",
  "source_epoch": "SOURCE_UUID",
  "record_class": "receipt_request"
}
```

Run `retention preview 1`. It records the bounded count for the first page. It makes no changes to markers or delivery data. Review the count and resource load. Run `retention apply 1` only for an apply-enabled grant. Each invocation runs one short transaction. The client sets a three-second statement limit and 250 ms lock limit. The persisted cutoff and the application/expiry/key index select the oldest remaining eligible rows without an offset. Deletion and the count/outcome checkpoint commit together. Both operations check current authority, epochs, global migration/seal and the legal hold. A mismatched frozen expiry, lock failure or failed checkpoint aborts the batch.

Repeat the same number after a lost response. Committed steps return their stored count without deleting another page. A `more` outcome allows a preview of the next contiguous step. `complete` means the page was shorter than the batch size. `budget_exhausted` means the final permitted page was full, so the remaining cardinality is unknown. A separate grant is required for more work. Two jobs in the same scope serialize page selection. The second job may apply fewer rows than its preview. A batch can never delete more than its preview count. Monitor WAL and replica lag, lock waits, statement timeouts, jobs held or denied and retained backlog. Pause grant issuance or set the scope hold when resource thresholds exceed the deployment's measured budget. Alert on nonzero command exit and reconcile with batch evidence; the command deliberately emits only a batch number, outcome and count.

## Excluded classes and release dependencies

M04 already handles expired send idempotency keys when an authenticated staff request reuses a key under the project lock. A background sweep needs its own history floor and hold behavior, including scheduled, processing and delayed replay cases. It has no retention grant class. All campaign detail, pending work, unresolved quarantine, reconciliation, unpublished events, binding history, audit data, revoked credentials, backups and erasure evidence stay outside this command.

No approved business lifetimes, archive/read contract, user erasure procedure, key-destruction plan or legal-hold registry is provided here. These require data-owner decisions and matching implementation. User erasure must reconcile active leases and pending work, preserve non-reusable scoped identities, propagate a versioned deletion to dependent consumers and prevent restore resurrection. Archive work needs a checksummed catalog, visibility boundary, retained aggregates, consumer/replay rules and tested reads after detail deletion. Backup/WAL expiry needs recoverable chains, an approved RPO/RTO and an erasure/revocation ledger replay before unsealing. Review staging index-build costs, batch latency/WAL and the disaster recovery procedure before production activation. See [ADR-10](../adr/adr-10-retention-recovery.md) and the [sealed restore drill](sealed-restore.md).
