# Per-binding FCM activation and hold

A successful mobile enrollment stores a protected binding. To deliver through FCM, the current binding generation also needs an applied route decision for its application and the current database origin and source epochs. An absent decision means enrolled only. A new owner, logout/login transfer or replaced destination cannot inherit an older generation's authorization. Existing pending delivery rows without a recorded audience route revision stay fenced.

## Approve an exact transition

Use a separate control-plane identity to verify a signed enrollment, the intended cohort, proof of consent and exclusion from the legacy sender. Record the immutable review artifact under a restricted evidence UUID. In a deployment with independently scheduled OneSignal campaigns, the rollout owner must verify exclusive routing or pause those campaigns. The database cannot observe the external provider's schedule. The [application lifecycle](application-lifecycle.md) and global dispatch holds apply independently.

The database owner binds dedicated issuer and executor groups to the approved deployment logins. Grant neither group membership in the function owner, table ownership, schema creation, or rights through other runtime groups. Preserve independent control over issuer and executor credentials. Only an authorized control-plane inventory can resolve the internal binding ID to its immutable installation, owner epoch, application and eligible audience. Do not export proof verifiers or addresses to the operator.

Use the following minimum grants for **existing, independently provisioned** roles. Recheck inherited permissions and membership on their actual logins. Runtime roles have no write access to these tables or execute access to the transition routine.

```sql
GRANT USAGE ON SCHEMA public TO hello_binding_issuer, hello_binding_operator;
GRANT INSERT (id,application_id,binding_id,operator_role,origin_epoch,source_epoch,
              expected_revision,destination,cohort_id,evidence_id,reason,resume_from,expires_at),
      SELECT (id), UPDATE (revoked_at)
ON public.binding_route_grants TO hello_binding_issuer;
GRANT EXECUTE ON FUNCTION public.apply_binding_route(uuid,uuid) TO hello_binding_operator;
```

The issuer inserts one expiring grant per reviewed binding. Bind the grant to the executor's **login OID**, both database epochs, the exact application and binding, the expected prior route revision, target state, cohort, evidence reference and reason. An unused grant can be revoked by setting its `revoked_at` to database time. Keep grant IDs out of broadly accessible logs.

| State requested | Required evidence and behavior |
| --- | --- |
| FCM_ACTIVE, pilot or expansion | The project must be ACTIVE, the runtime open, and the binding live with the same active owner. No decision yet has revision zero. Each applied transition increments the revision. |
| HELD, incident or rollback | Stops future charges and submissions for this binding, including already charged work waiting on provider credentials. The attempt remains charged. An external call that already started may still reach the device. |
| FCM_ACTIVE, revalidation | Set `resume_from` to the original approved audience revision. Its binding, cohort and database epochs must match the new decision. Only a new reservation under the current authorization can submit. Activating a new revision without this field leaves earlier pending intents fenced. |
| RETIRED, retirement | Final for this binding. The evidence and recorded results remain available, and future attempts cannot reactivate it. |

The identified executor calls the function with the grant and public application UUID using its own authenticated connection.

```sql
SELECT public.apply_binding_route(:'grant_id'::uuid, :'application_id'::uuid);
```

Use a short read-committed transaction. The application row orders changes against enrollment, acceptance and per-attempt checks. A successful retry of an already applied grant returns its historical revision. Read current state separately from the protected inventory. A mismatch, expired grant or unavailable review must leave the affected work held. The down migration refuses to discard approvals or frozen audience revisions.

## Coverage and revalidation

Immediate acceptance and fan-out check every active modern binding in the targeted population. An enrolled-only, held, retired or foreign-epoch current binding blocks coverage even when the same user also owns another approved binding. Active legacy destinations and eligible users without a modern binding also block full coverage. A pilot should explicitly target only approved test subjects. Every applicable device for those subjects must satisfy the checks. Full broadcast requires independent evidence that the backend population includes the intended eligible devices.

Preparation freezes the initial route revision in every delivery intent. Reservation checks the current authorization, and the worker checks it again after credential or quota waits just before the physical provider call. A new approval cannot rescue an old in-memory reservation. Uncertain or late provider outcomes and authenticated device receipts retain their original binding and campaign identity. See the [worker procedure](delivery-worker.md) and [mobile cutover register](../analysis/cutover-readiness.md).

The isolated PostgreSQL integration test `TestBindingRouteApprovalFencesAttemptsAndSnapshots` exercises a non-owner executor, foreign and stale approvals, enrollment-only coverage, hold, explicit resume and terminal retirement. Load fixtures seed synthetic authorization history for the measured cohort. Actual mobile builds, legacy exclusion, independent evidence custody and operator access require target verification.

## Read-only coverage inventory

Run the [coverage query](../../scripts/sql/cutover-coverage.sql) with an authorized inventory login against one application before approving a cohort or full broadcast. Use a verified-TLS service profile, trusted schema and a read-only transaction with a deployment-sized timeout. For example, `PGOPTIONS='-c default_transaction_read_only=on -c search_path=pg_catalog,public -c statement_timeout=15000' psql -X -v ON_ERROR_STOP=1 -v project="$PROJECT_UUID" -f scripts/sql/cutover-coverage.sql service=notification_inventory` returns one timestamped JSON record. An unknown project returns no record. Run the separate ownership preflight to account for globally unassigned users.

The denominator is the application's active users. The report counts their owned installations and modern bindings, users represented in both registries, active legacy registration rows and users with no current address. Ownerless installations are counted separately. Group entries identify their unit, platform, cohort and state. Only bounded numeric legacy app versions are exposed. The modern enrollment contract has no app-version field. Group output is capped at 200; check `groups_truncated` before using the breakdown. Totals include every group. Physical-device eligibility and consent exclusions are null until an authorized source supplies those facts. Add no legacy and modern counts together to infer a device census. The report contains no user identity or provider addresses. Preserve the snapshot with the independently reviewed legacy-exclusion and consent evidence.
