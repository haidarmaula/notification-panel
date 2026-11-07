# Database roles for the protected scheduler and workers

The owner-applied template covers the durable scheduler, the admission-only
worker and the application-configured delivery worker. It requires PostgreSQL
17, migration 000035, an owner-controlled `public` schema and three **dedicated
NOLOGIN group roles**. Supply a distinct, separately issued database login in
each daemon's protected configuration. Run the template again after installing
migration 000037 so route readers and prepared intents use the approved route
decision. The companion file is loaded relative to the main template.

A security administrator provisions these ordinary groups in the reviewed
target cluster. The template verifies they exist, own no objects, hold no other
roles, and lack login, replication, bypass-RLS, superuser, database creation,
role creation and schema creation privileges.

```sql
CREATE ROLE hello_scheduler NOLOGIN;
CREATE ROLE hello_admission NOLOGIN;
CREATE ROLE hello_delivery NOLOGIN;
```

With a reviewed target database and a trusted owner connection, apply from a
file so psql can resolve the M37 companion. Run the same command again to
verify repeatability. Before assigning any login, inspect the complete role
membership graph and role defaults, database/schema access, function owners,
extension schemas, object ACLs and future default privileges.

```bash
psql -X -v ON_ERROR_STOP=1 --file=deploy/postgres/daemon-grants.sql
```

The transaction checks existing effective table and column grants in the
application schema. It rejects unexpected grants, grant options on these
objects, and unexpected executable security-definer routines before applying
the privilege specification. The M35 down migration refuses to remove identity
guards while non-owner UPDATE grants on their lock columns survive. Before an
approved downgrade, stop the daemons and revoke those grants under owner control.
The template never creates logins or assigns memberships.
Resolve drift with the database and security owners. Use separate credentials
for schema ownership, migrations, backups, route issuance, administrative
replay and retention.

| Group | Runtime work | Data and authority boundaries |
|---|---|---|
| `hello_scheduler` | Promote schedules, write and drain the send outbox, recover queued preparation and expire abandoned replay claims | Can append audit evidence. No audit-history read, provider token ciphertext, campaign payload, replay claim creation, operational grant issuance or delivery-update drain. |
| `hello_admission` | Lock source and runtime gates, prepare an accepted audience, quarantine original Kafka bytes under their source position | No staff credentials, provider address bytes, installation proof verifier, send/update drain, delivery attempt charge, or replay administration. |
| `hello_delivery` | Validate the active route, reserve an attempt, read encrypted destination material, record and recover outcomes, drain delivery updates | May retire a binding, increment its installation version and request a one-way application hold. It cannot release that hold, transfer ownership, rewrite the source registration, read the installation proof verifier, or delete the send outbox. |

All three groups can read the columns needed by role readiness, status counts
and queue-age metrics. PostgreSQL row locking also needs UPDATE privilege on
one column. The grants use immutable identities and the singleton constrained
to true. M35 enforces the identity invariants on the affected rows. A worker
cannot use a lock-only UPDATE to rewrite a business identifier. The protected
hold routine only sets the hold bit. Replay cleanup routines check the original
position, claim, expiry or finished outcome. They do not authorize publication.

Scope is enforced in the application transactions by source, application,
ownership, lease and attempt. PostgreSQL table/column grants constrain which
operations a credential can perform across role boundaries. Raw access to the
allowed DML can affect multiple rows, including other applications in the same
database. Review tenant-level database isolation/RLS separately if required.
The scheduler's send-outbox DELETE and the delivery worker's update-outbox
DELETE apply to their respective relations. They belong only to trusted runtime
logins. Protect the issuer, login, schema search path, tenant configuration and
database connection as part of the deployment boundary.

## Disposable verification

```bash
GOCACHE=/tmp/hello-go-review-cache bash scripts/daemon-grants.sh
```

The wrapper uses the local pinned PostgreSQL image, no network, a tmpfs data
directory, an ordinary database/object owner and four non-owner logins over a Unix socket. It applies all current
migrations, applies grants twice, injects an unsafe extra grant and verifies
that the owner template refuses it. The exercised flows include a promoted
schedule, send acknowledgement, audience preparation, duplicate quarantine,
expired replay cleanup with append-only audit, attempt reservation, recovery,
campaign completion, delivery-update acknowledgement and configuration hold.
Kafka acknowledgements use an in-process recorder. The DB access checks use
real PostgreSQL sessions and the repository's normal service methods. The
fixture lifts the application provisioning-only constraint for its synthetic
application. Target activation and authentication policies require their own
approval.

The negative probes cover direct unseal, hold release, credential reads,
ownership writes, replay and route issuance, cross-role outbox mutations,
table creation/alteration and truncation. Re-run with the exact migration and
binary revision in the release. Verify mTLS or the approved database
authentication, network ACLs, separate login membership and effective privileges
for every actual process and the roles outside this template before enabling
traffic. [Security ADR](../adr/adr-11-security-baseline.md)
