# Offline application enrollment and retirement

Migration 000038 keeps the original `PROVISIONING` default and constraint.
The registry HTTP API creates metadata and explicit staff grants. A deployment
can enable this separate lifecycle procedure after the reviewed ownership
cutover. The installer changes the permitted states without activating a
project or granting authority to a runtime login.

## Before installation

1. Record the reviewed image/credential inventory, ownership mapping and
   backups. Drain and fence every legacy server, scheduler, worker, replay
   channel and direct writer. Verify termination of prior sessions, Kafka
   consumers, provider access and restart paths. Complete the ownership
   backfill and run the eleven-table ownership finalizer. Use an offline
   maintenance window and bounded DDL lock timeouts. [Ownership cutover](../specs/m03b-cutover.md)
2. Record cohort ownership, installation identity, provider route assignment
   and legacy-exclusion evidence for the specific project. The reviewer
   retains the reviewed evidence artifacts under access control and computes
   their SHA-256 digests. Account for already-started or uncertain submissions.
3. The DBA provisions two non-owning NOLOGIN groups,
   `hello_application_lifecycle_issuer` and
   `hello_application_lifecycle_operator`. Use separate restricted LOGIN
   identities. Each login inherits exactly its intended operational group.
   Keep the schema owner, service logins and deployment credentials separate.
   Pin the PostgreSQL server identity, database and secure connection settings
   in the approved psql service profiles.
4. As the trusted schema owner, install the grants and the status guard.
   The grants profile checks existing effective table/column privileges and
   security-definer functions for its two groups. The installer rejects
   unresolved ownership and existing unreviewed active projects. Repeating
   installation preserves the current status of every project.

```sh
psql -X service=schema_owner -f deploy/postgres/application-lifecycle-grants.sql
psql -X service=schema_owner -f scripts/sql/install-application-lifecycle.sql
```

## A single transition

The reviewer records the public project UUID, current status and revision, the
runtime origin and source epochs, a fresh grant UUID, executor login and
expiry. The maximum lifetime is 24 hours. Supply the actual reviewed retired
PostgreSQL writer login/group names. An empty array represents a documented
greenfield deployment with no previous database writers. Each listed role
must be absent or NOLOGIN without login members or live database sessions
when activating. The database verifies the catalog constraints and current
FCM binding routes. The reviewer verifies the external deployment, Kafka,
provider and client cohort fences represented by the three evidence digests.

The project needs an explicitly provisioned dispatch policy. Before activating,
verify `hold_new=true`, `coverage_approved=false`, and no current-epoch
`FCM_ACTIVE` binding route. Newly active enrollment can use the configured
mobile identity/proof flow. Existing scoped staff grants retain their
documented business permissions. The lifecycle operation creates no staff
grant, route approval, send request or credential. The runtime's seal and
migration hold keep their current values.

```sh
psql -X service=lifecycle_reviewer \
  -v grant_id="$GRANT_ID" -v project="$PROJECT_UUID" -v executor="$EXECUTOR_LOGIN" \
  -v origin="$ORIGIN_EPOCH" -v source="$SOURCE_EPOCH" -v revision="$CURRENT_REVISION" \
  -v from_status="$CURRENT_STATUS" -v destination="$DESTINATION" \
  -v ownership_sha256="$OWNERSHIP_DIGEST" -v writers_sha256="$WRITER_FENCE_DIGEST" \
  -v cohorts_sha256="$COHORT_FENCE_DIGEST" -v retired_roles="$RETIRED_WRITER_ARRAY" \
  -v expires_utc="$EXPIRY_UTC" -f scripts/sql/issue-application-lifecycle.sql
psql -X service=lifecycle_executor -v grant_id="$GRANT_ID" -v project="$PROJECT_UUID" \
  -f scripts/sql/apply-application-lifecycle.sql
```

The issuing login can revoke an unused grant through
`revoke_application_lifecycle(grant_id, project_id)`. Execution locks the
runtime lineage and project, rechecks the exact grant, current revision and
policy, records its application time and increments the project revision in
one transaction. Acknowledgement loss permits retrying the same grant on the
same project and epochs. The returned revision identifies the original
transition, even when later changes have advanced the project. A restored
lineage, stale revision, wrong login or wrong project cannot reuse the
original approval for a fresh transition.

`PROVISIONING` can move to `ACTIVE`, `HELD` or `RETIRED`.
`HELD` can move to `ACTIVE` after fresh review or to `RETIRED`.
An active project can move to `HELD` or `RETIRED`. Both operations atomically
set `hold_new=true` and `coverage_approved=false` for the project. Already
authorized provider calls may finish. Reconcile their recorded attempts using
the worker recovery procedure. `RETIRED` is terminal. Retain the immutable
grant and outcome history with its external evidence. Migration rollback
refuses retained grants or a nonprovisioning project.

## Local evidence

`bash scripts/application-lifecycle.sh` runs the migration cycle, owner
installer, real reviewer/executor SQL and role-denial checks on the pinned
PostgreSQL 17 image with container networking disabled. It creates two
synthetic projects. Overlapping retries produce one enrollment transition.
Wrong project, actor, epoch, revision, expiry, revoked grant, login membership,
unfenced cohort, unsafe dispatch policy and direct writes are rejected.
The accepted project goes through ACTIVE, HELD and RETIRED with exactly
three committed application transitions. The other project stays at
PROVISIONING revision one. The runtime remains sealed and migration-held.
Deployment acceptance requires the actual reviewed writer and cohort
inventory, login grants, recovery rehearsal and target policy.

A separate reviewed grant is required before an enrolled project can submit
a pilot or full campaign. [Dispatch release and rollback](application-dispatch.md).
