# Staff session families

Migration 000033 binds staff authentication to a database family. The family has a random UUID, one SHA-256 digest of a random 32-byte refresh token identifier, a generation number, an absolute expiry, an account authentication epoch and the session recovery epoch. The database keeps the rotation and explicit revocation timestamps/reason. Login secrets, JWTs and unhashed refresh identifiers are excluded from persistence and logs.

HS256 verification pins the key and purpose for access or refresh, verifies time, and requires a positive numeric staff identity, matching decimal subject, canonical nonzero family UUID and a 32-byte token identifier. The issuing service signs a candidate pair and returns it only after persisting the initial digest or successfully advancing the expected digest. The staff row is locked and its epoch rechecked during issuance. A changed password, role or active flag advances the epoch in the same transaction, including changes issued outside the HTTP server. Deletion detaches the family so reuse of a numeric staff ID cannot reactivate it.

Rotation orders locks by recovery control, staff account and family. Under read committed isolation a waiting refresh checks the current locked digest. A reuse commits family revocation. Concurrent requests cannot produce two usable successors. An already-issued access token can remain valid across normal rotation until its own expiry. Logout and reuse invalidate all access tokens of that family. Current account and role/grant checks still apply. Request authorization is a statement snapshot. Already-running operations must enforce their existing write-time authority guards.

Credential responses carry `Cache-Control: no-store`. The HTTP contract remains a server-to-server JSON bearer exchange. The client must serialize refresh and replace both tokens, then reauthenticate after uncertain rotation. The policy deliberately trades a second login after a lost refresh response for immediate reuse detection. See the [API contract](../api/auth-api.md) and [RFC 9700 §4.14.2](https://www.rfc-editor.org/rfc/rfc9700.html#section-4.14.2).

## Sealed recovery

Before exposing any API traffic to a restored database, independently fence the old writer and restrict server, admin and maintenance access. Verify the restored `notification_runtime` seal and migration hold and the intended database identity. Reconcile staff password, active status, role/grant, offboarding, MFA and signing-key changes from the authoritative record outside the restored database. Rotate `staff_session_control.session_epoch` to a fresh UUID in a transaction while the database remains sealed. Record the old and new UUID and operator evidence in the incident manifest. The rotation invalidates both access and refresh for every historical family in constant work. Confirm old credentials receive 401 before opening ingress. Each fresh login pins the new epoch. A restored control row retains its old epoch until this procedure runs.

The sealed administrative operation can compare the reviewed pre-rotation UUID to prevent an accidental second rotation on retry. Supply the reviewed old and new UUIDs as bound parameters. Require exactly one updated row, or reconcile the committed epoch with the prior signed incident record before continuing.

```sql
WITH gate AS MATERIALIZED (
    SELECT singleton FROM notification_runtime
    WHERE singleton AND sealed AND migration_hold FOR SHARE
)
UPDATE staff_session_control c SET session_epoch=$2::uuid
FROM gate
WHERE c.singleton=gate.singleton AND c.session_epoch=$1::uuid
    AND $1::uuid<>$2::uuid AND $2::uuid<>'00000000-0000-0000-0000-000000000000'
RETURNING c.session_epoch;
```

A backup or a past login is insufficient evidence that an account remains authorized. Production issuer/audience and key-ring policy, corporate MFA/SSO, BFF and cookie/CSRF approvals, login throttling budgets, session evidence retention, recovery reconciliation and actual client cutover remain deployment gates. Review [ADR-11](../adr/adr-11-security-baseline.md) and the [sealed restore runbook](../runbooks/sealed-restore.md).
