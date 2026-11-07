# Releasing a managed project for dispatch

Fresh projects start in PROVISIONING. The offline application lifecycle installer
and its reviewed activation grant enable enrollment while the dispatch policy
remains HELD. Migration 000039 places previously installed projects on hold
until they receive a fresh dispatch approval. No runtime login receives
release authority. [Application lifecycle](application-lifecycle.md).

## Review and role setup

1. The deployment owner verifies the database identity, project UUID and
   revision, current dispatch mode/revision, and origin/source epochs. Verify
   that recovery is complete and the runtime is open. Unsealing or releasing a
   migration hold uses the separate target-owner recovery procedure.
2. The reviewer verifies the stored binding route decisions, owner/epoch,
   cohort and active current-generation devices. Retain four reviewed artifacts
   covering legacy sender exclusion, consent, population coverage and bounded
   quota. For a pilot, record the exact cohort UUID and exclude every other
   recipient. For full broadcast, reconcile the eligible denominator, legacy
   destinations and unclassified or missing devices against the owner's
   approved scope. Record peak limits across replicas, account for overlapping
   rollouts, and verify provider/project allocation. Store the artifacts in the
   approved evidence system and record their SHA-256 digests. A digest records
   the reviewed artifact; the reviewer validates its contents.
   [Coverage inventory and binding routes](binding-route-cutover.md).
3. The DBA provisions separate, non-owning NOLOGIN groups `hello_dispatch_issuer`
   and `hello_dispatch_operator`. Assign distinct, restricted LOGIN identities
   to the respective groups. Issuance and execution verify both logins still hold their designated authority and lack elevated role attributes or protected write privileges. Keep their credentials separate from the schema
   owner, scheduler, admission and delivery roles. As the trusted schema owner,
   apply the protected grants profile.

```sh
psql -X service=schema_owner -f deploy/postgres/dispatch-policy-grants.sql
```

## Pilot, full release and rollback

The reviewer issues a single grant bound to the executor's database login OID,
the project and both revisions, its previous mode, destination, reason and the
runtime epochs. Each grant expires within 24 hours. The applicable binding
cohort must contain a currently approved FCM route in those epochs for a live
installation and active owner. Use environment-specific TLS service profiles.

| Destination | Prior mode and evidence | Result |
| --- | --- | --- |
| PILOT | HELD, PILOT or FULL, with a named reviewed cohort and reason `pilot` or `revalidation` | Individual and segment campaigns must resolve entirely within the cohort. Broadcast and empty pilot audiences are rejected. |
| FULL | Current PILOT, same reviewed cohort and epochs, separate grant with reason `full_coverage` | Broadcast becomes eligible for target-specific coverage checks. All applicable bindings still require individual M37 route authorization. |
| HELD | Any current mode, including HELD, with reason `incident` or `rollback` and an empty cohort | Stops new acceptance and physical starts, clears approval and advances the generation. Earlier pending grants become stale. |

```sh
psql -X service=dispatch_reviewer \
  -v grant_id="$GRANT_ID" -v project="$PROJECT_UUID" -v executor="$EXECUTOR_LOGIN" \
  -v application_revision="$APPLICATION_REVISION" -v dispatch_revision="$DISPATCH_REVISION" \
  -v from_mode="$CURRENT_MODE" -v destination="$DESTINATION" -v cohort="$COHORT_UUID_OR_EMPTY" \
  -v origin="$ORIGIN_EPOCH" -v source="$SOURCE_EPOCH" -v reason="$REVIEW_REASON" \
  -v legacy_sha256="$LEGACY_DIGEST" -v consent_sha256="$CONSENT_DIGEST" \
  -v coverage_sha256="$COVERAGE_DIGEST" -v quota_sha256="$QUOTA_DIGEST" \
  -v expires_utc="$EXPIRY_UTC" -f scripts/sql/issue-application-dispatch.sql
psql -X service=dispatch_executor -v grant_id="$GRANT_ID" -v project="$PROJECT_UUID" \
  -f scripts/sql/apply-application-dispatch.sql
```

The issuer can revoke an unused grant with
`revoke_application_dispatch(grant_id,project_id)`. Retry an ambiguous apply
using exactly its original grant and project. The returned revision identifies
the committed transition. Read the current policy independently. A historical
success cannot reopen a held project, and a pending older grant cannot cross
an intervening hold, including a second incident while already held.

Application status, runtime epochs and seal, dispatch mode, binding route
approval, audience ownership and attempt budgets all remain enforced.
Preparation rechecks the entire pilot audience before inserting intents.
Only a preparation frozen under the same pilot cohort can send during PILOT. The stored full-coverage flag stays false during PILOT so an earlier binary cannot treat its restricted release as full coverage. The worker rechecks the current binding cohort and the dispatch generation
after provider credential or quota waits. Calls already started may settle.
The existing one-way FCM error hold and a project lifecycle hold atomically
advance the dispatch revision and clear the old release. Reconcile charged
or uncertain attempts before issuing fresh review for another generation.
Migration rollback refuses applied or outstanding grant evidence and any
active managed release.

## Local evidence

Run `bash scripts/application-lifecycle.sh` against the pinned local PostgreSQL
image. The disposable container has no network interfaces beyond loopback and
publishes no ports. Real issuer and executor logins exercise concurrent retries,
wrong operator/project/revisions, a foreign cohort, separate full approval,
one-way provider holds and owner-reviewed rollback. A second project and the
global runtime settings retain their scoped state. The integration suite
also exercises campaign acceptance, fan-out and physical-attempt authorization.
Deployment sign-off still requires the target credentials, quota and the
reviewed legacy, consent and coverage evidence.
