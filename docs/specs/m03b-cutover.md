# M03b.5 cutover and legacy retirement

This revision mounts only scoped business and global staff/control routes.
The legacy mobile sync, device management, users, segments and notification
routes are unmounted. The worker and scheduler entry points exit at startup.
The development stack starts the scoped server without either process.

Before running the ownership finalizer on a deployment:

1. Record backup and restore evidence, deployed image versions, approved
   project mapping, pending campaign state and Kafka offsets. Coordinate the
   client transition before withdrawing legacy routes.
2. Stop and drain old servers, workers, schedulers and replay or SQL writers.
   Revoke their database, Kafka and provider access and prevent their images
   from restarting. Reconcile in-flight or uncertain sends separately.
3. Run the count-only preflight and reviewed backfill in the maintenance window.
   Resolve or quarantine unmapped business rows and record the disposition of
   unresolved audits. Preserve intentionally global staff audit.
4. Run scripts/sql/finalize-application-ownership.sql as a single statement
   with a bounded lock timeout. It validates all eleven business ownership
   columns as NOT NULL and rolls back the entire statement if any row is
   unassigned. Re-run preflight and inspect constraints before releasing writers.
5. Deploy the scoped server and verify retired paths and scoped authority using
   the approved deployment identities. Keep projects PROVISIONING until scoped
   mobile identity, durable scheduling/admission and provider execution are ready.

The finalizer is an explicit operational step after data review. Standard
schema migration 000017 remains compatible with the ownership transition.
Global legacy external-ID and token uniqueness stay conservative until their
identity and address replacements are specified in M03c. The existing
composite references and ownership immutability constraints remain enforced.

Retirement in this revision cannot stop an already running older binary.
After business rows from multiple projects exist, rollback must use a scoped
binary or hold the system offline and restore a verified single-project backup.
The repository fixture verifies routing and structural constraints. Production
drain, mapping, credential revocation, restore evidence and activation require
deployment-specific verification.
