# M07 device receipts and campaign statistics

A receipt acknowledges one signed payload reported by an authenticated mobile
user. PostgreSQL remains authoritative for the current application, original
delivery, installation owner and owner epoch. A same-owner token refresh keeps
that relationship. Logout and account transfer revoke it. The verifier selects
the committed environment and key ID before distinguishing expiry.

Lock the runtime, application, active user, campaign, installation, original
binding and delivery in that order. Check the frozen signer metadata and current
lineage, including on duplicates. Use one bounded transaction for request
deduplication, the first fact per delivery/event, first OPENED user read, ledger
change and versioned observation outbox. A missing commit acknowledgement can
be retried with the same client event ID. OPENED implies observed delivery.
A later explicit DELIVERED can add its own first server timestamp. A FAILED
submission retains that disposition when a device reports a late observation.

Statistics use the preparation marker for the immutable audience counts and
the modern delivery ledger for outcomes. Return unavailable counts for a
missing marker and preserve known zero for a prepared empty audience. Count
provider acknowledgements, observations and failures separately. A late
observation of a failed submission contributes to both relevant counts.

## Verification

The isolated PostgreSQL and Kafka suite exercises both timing orders, retained
failure evidence, request conflicts, verified-key rotation fences, subject
revocation, owner transfer and switch-back, actual HTTP authentication and
rollback when the outbox rejects an observation. The statistics API tests cover
missing and known-empty snapshots, damaged ledgers and revoked grants.
The [mobile receipt API](../api/mobile-receipts.md) documents offline retry and
proxy policy. The [staff API](../api/scoped-campaign-history-api.md) defines count units.
