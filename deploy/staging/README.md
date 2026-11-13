# Single-VM staging preparation

Target: `kafka-vps` (`haidar@10.1.16.109:10022`, hostname `srv-kafka`).
These scripts extend the existing migration-41 PostgreSQL/Kafka setup. They do
not provision an empty host or provide production availability.

1. Run `prepare-host.sh` as root with the staged Firebase credential path.
   This creates dedicated TLS material, installs the credential, and removes
   the upload. This phase has completed on the designated VM.
2. Review and run `configure-api.py` with root privileges. It creates the
   restricted server login, protected policies, and staging administrator.
   The bootstrap container uses the existing migration identity for its
   one-shot operation; its policy is removed afterward. Generated administrator
   credentials remain in `/etc/hello/staging-admin.json`, mode `0600`.
3. Run `start-api.sh` as the Docker-enabled SSH account. This validates Nginx
   configuration and starts the API and gateway using pinned images. Existing
   named application containers cause an explicit stop for inspection.

The gateway listens at `https://10.1.16.109:8443`. Install the public
`/etc/hello/staging-tls/server-ca.crt` in test-device trust stores through an
approved mechanism. Do not distribute CA keys. The gateway accepts RFC1918
source addresses; network routing and upstream firewalls must restrict actual
reachability to office/VPN clients. Verify device access separately.

The API listens on loopback with TLS client authentication. Gateway and API
containers run as UID/GID 65532, with read-only filesystems, dropped capabilities,
bounded resources and rotated Docker logs. Current limits are conservative
starting values, not measured capacity. The API has no browser-origin allowlist
or mobile policy yet. Worker policies, application provisioning, Kafka source
registration/review, offsets, real-device acceptance, backup/restore, healthchecks
and operational monitoring remain separate work. Dispatch holds remain enabled.

The runtime images are from `4695a2c`; the two subsequent commits at `90749a9`
change onboarding documentation/checks. Migration 41 and the server grant
template are currently local uncommitted work already applied to this VM;
reconcile and retain them before promoting the deployment elsewhere.

## Verified progress on 2026-11-12

The API and gateway are running; direct office-network HTTPS passed with the
staging CA after the operator added UFW rules. VPN-only access from outside the
office is still unverified. `smoke-auth.py` passed login, API-key enforcement,
protected listing, refresh rotation, logout and revoked-access rejection using
the restricted runtime database identity. Its `--provision-app` mode created
`hello-staging`, public UUID `63677e77-2661-43b8-8253-35c1134bcc60`, in
`PROVISIONING`. No staff application grant was created.

`register-inactive-sources.sql` registered both observed Kafka logs inactive.
The restricted review manifest is on the VM at
`~/hello-staging-provision/kafka-source-review.json`. All three send partitions
had earliest/latest offsets zero; the admission group was absent. Numeric zero
offset resets passed dry-run. The project owner subsequently approved the exact
identities and zero offsets in this chat on 2026-11-12. Both exact source rows
were activated and all three group checkpoints initialized to zero, with zero
lag verified. The restricted manifest records the approval and application time.

`prepare-daemons.py` seeded separate root-owned protected policy volumes for
scheduler and admission. Network-isolated startup checks passed policy loading
and then failed at runtime with network access disabled. Neither daemon was
started persistently at this preparation stage. Policies pin the observed
cluster/topic identities. After source approval, attempted persistent startup
failed because `SourceManifest.VerifyRegistration` rejects a sealed runtime.
Both daemon containers were stopped to end restart loops. Their exact database
policies passed TLS, login and source SELECT probes. `sealed=true` and
`migration_hold=true` remain unchanged; the startup script now refuses sealed
runtime state before creating containers. The API/gateway remain running.

The Android app at `~/projects/hello-fcm-smoke` uses package
`com.example.hellofcmsmoke` and the staging Firebase project. Its installation
enrollment/refresh/logout requests match the backend by source inspection.
ESLint and TypeScript checks passed; existing app edits were preserved. Device
compatibility remains unverified: the build needs staging CA trust, the API URL
and application UUID, an enabled mobile policy, an active application/test
identity and a staging issuer. Its existing local issuer is loopback-only and
local-test-only. The app displays notifications but does not post delivery/open
receipt observations, so full receipt acceptance is not yet supported.

## Android preparation after source approval

The active server now uses protected volume
`hello-staging-server-mobile-policy`, including its matching smoke enrollment
policy. The previous API container is stopped as
`hello-staging-server-pre-mobile` for rollback. Mobile enrollment rejects
unauthenticated HTTPS requests with 401; the API returned after schema prep.

The ownership finalizer and lifecycle grant/guard installation passed under
an API drain, bounded locks and transactional assertions that all eleven
business tables were empty. A held dispatch policy and one synthetic user
(`staging-android-smoke-haidar`) were subsequently provisioned for the staging
app, with namespace `hello-staging-mobile` and subject `android-smoke-haidar`.
Separate restricted lifecycle logins were provisioned by
`prepare-lifecycle-identities.py`; their root-only credential volume is not
mounted in any runtime service. No lifecycle grant was issued or applied.

The exact proposed enrollment activation is on the VM at
`~/hello-staging-provision/android-enrollment-review.json`. Application status
remains PROVISIONING, runtime remains sealed/migration-held, and application
dispatch remains HELD. Reviewer approval for enrollment activation is pending.

The Android project has an opt-in staging CA plugin and configured environment
defaults. The arm64 debug APK built and installed on the owner's wireless-ADB
phone. The running app shows the staging UUID and an unenrolled state; old
local credentials remain in their original storage namespace. ESLint,
TypeScript and plugin resource checks passed. Metro uses localhost port 8083;
the temporary loopback issuer is prepared for port 8181 but has not been
started. See `~/projects/hello-fcm-smoke/STAGING.md` for reproducible settings.
At that preparation stage, phone enrollment and FCM delivery were unverified.

## Approved Android enrollment — 2026-11-12

The owner approved enrollment activation in this chat. The exact lifecycle
grant was issued and applied through distinct restricted reviewer/executor
logins, producing application revision 2 with status ACTIVE. Restricted
ownership/writer/cohort evidence files and their SHA-256 hashes are retained
alongside `android-enrollment-review.json` on the VM. The runtime seal,
migration hold and HELD application dispatch policy were not released.

The owner's Android phone enrolled over staging HTTPS through the temporary
ADB-forwarded issuer. Database counts confirmed one installation and one active
binding, and the app reported enrolled. An initial HTTP 500 exposed the missing
origin-conflict read grant (STG8); the disposable PostgreSQL fixture reproduced
it and passed after the three-column fix. The corrected grant profile was
applied to staging. No provider execution or notification campaign was started.
The enrolled phone's refresh operation also passed, without creating a second
installation or binding. Final state remained ACTIVE revision 2, sealed and
migration-held, with HELD application dispatch.

For this device's debug runtime, the default Metro reverse port also needed
`adb reverse tcp:8081 tcp:8083`. Only its regenerable cached debug bundle was
removed during reload; enrollment/Firebase data were preserved. The Metro
instance now runs without CI mode. The app includes a reset action for failed
pending pairings, and current source passes lint and typecheck.

## One-device notification pilot prepared

Campaign 1 is DRAFT, titled `Hello staging`, with body
`Staging push test to Haidar's Android phone.` Its only target is the enrolled
synthetic Android user. The owner confirmed in this chat that this smoke app
is excluded from other OneSignal/FCM campaigns during the test. The bootstrap
administrator now has an explicit grant for this staging application.

`prepare-delivery.py` created the protected delivery policy volume, preserving
the enrollment address keys and generating a distinct receipt signer. Its
limits are one replica, one concurrent request, one request/second and one
attempt per delivery intent. Offline startup passed configuration loading and
stopped with network access disabled. `verify-fcm-credential.py` authenticated
with Google for Firebase Messaging scope without sending a notification;
actual send authorization remains to be exercised.

`prepare-pilot-identities.py` provisioned separate restricted binding and
dispatch reviewer/executor logins. The protected dispatch grant profile is
installed. No binding or dispatch release grant has been issued. The exact
target, campaign, cohort proposal, limits and procedure hashes are in
`~/hello-staging-provision/pilot-send-review.json`; sending approval is pending.

The target-owner `open-approved-pilot.sql` checks the exact greenfield database,
lineage, schema, single DRAFT target, one live device and empty queues before
opening the runtime. Its rollback-only dry run passed and committed no hold
change. Recheck live Kafka identities and zero backlog immediately before any
approved execution. Opening, binding activation, PILOT approval and campaign
acceptance remain separate gates. Use the existing distinct-role grant
procedures; never substitute direct route or dispatch release writes.

Execution must always restore holds using `close-pilot.sql`, including on
failure. Stop and join scheduler/admission/delivery before reporting closure;
if closure hits a lock timeout, stop processing, retry and verify runtime seal,
migration hold and application HELD state. Keep the same campaign/idempotency
identity after ambiguity. The single-attempt limit does not establish exactly
once external delivery. A notification already in flight can still complete.

The pilot can establish provider acceptance and Android display. The new
receipt signer's public key is prepared in the worker volume but is not yet
installed in the server verifier policy; the app also does not post receipt
observations. Full receipt ingestion remains separate work.

## One-device pilot passed — 2026-11-12

The owner explicitly approved sending draft 1 to the enrolled Android phone,
temporarily opening staging and restoring all send blocks. The runner verified
Kafka identities and zero backlog, applied the exact binding/PILOT approvals
through separate control logins, and started the three processing roles.
Campaign 1 was accepted using its stored idempotency key, reached COMPLETED,
and produced one SENT intent with `attempt_count=1`. The foreground Android app
displayed `Hello staging` and `Staging push test to Haidar's Android phone.`

Application dispatch is HELD again; runtime is sealed and migration-held.
Scheduler, admission and delivery containers were stopped and verified stopped.
The API/gateway remain available. No second campaign or send was submitted.

After successful send/cleanup, a startup-log loop variable had shadowed the
watchdog marker Path, failing the final bookkeeping write. The loop variable
was renamed. The authoritative database result and stopped-container/hold
checks were independently re-read, the watchdog was disarmed, and the result
record repaired without rerunning the send. Private records are
`~/hello-staging-provision/pilot-result.json` and `pilot-send-review.json`.

This establishes provider acceptance and foreground display for one Android
device. It does not establish receipt ingestion, background/tap/cold-start
coverage, multi-device capacity, VPN-only reachability or production readiness.
The pilot opening guard intentionally rejects the now-completed campaign and
retained grants. Do not rerun it to send another notification; later work must
use the current reviewed state and dispatch generation.

## Manual Postman window — 2026-11-12

After the owner requested reopening for Postman campaign 2, the existing
one-device cohort was revalidated through distinct dispatch identities.
`open-postman-window.py` opened a 30-minute PILOT window and started the three
processing roles. It submitted no campaign. The acknowledged independent
watchdog restores holds and stops processing at 15:30:56 WIB on 2026-11-12;
records are in `~/hello-staging-provision/postman-window-*.json` and the current
status file. This permits eligible manual campaigns to user 1's enrolled device,
rather than limiting the entire window to one send. Automatic closure retries
safe hold restoration after stopping processing; inspect the watchdog log if
status is `closure_failed`. This historical note is not a live status check.

## Routine one-device staging pilot

The owner explicitly requested routine staging operation after the timed window
closed. `enable-routine-pilot.py` verified the closed timers, unchanged lineage,
ACTIVE application, current one-device FCM route and absence of pending sends.
It applied a fresh restricted-identity dispatch revalidation grant, retaining
the existing approved cohort, and started scheduler, admission and delivery.
All three roles use `restart=unless-stopped`. No campaign was submitted by the
activation script. The standing configuration has no automatic expiry.

The current approval and status are recorded privately on the VM in
`~/hello-staging-provision/routine-pilot.json`. This is PILOT, not a full
population release: new/replaced/foreign-owner bindings need their own valid
route approval. API authorization, provider limits, idempotency and one-way
incident holds continue to apply. Earlier time-window records remain closed.

The owner can pause sending and stop/join processing without changing enrolled
devices or shutting down the API:

```sh
ssh kafka-vps 'python3 ~/hello-staging-provision/stop-routine-pilot.py'
```

This restores application HELD plus runtime seal/migration hold. Existing
in-flight provider calls may complete before shutdown joins. A later reopening
must inspect the then-current dispatch generation and any pending work; never
clear an incident hold blindly. Container restart policies are configured;
an actual VM reboot recovery test remains unperformed.

## Receipt verifier and Android reporting

`enable-receipts.py` installs the existing delivery worker's public Ed25519
receipt key into the active server mobile-policy volume. It preserves mobile
and origin keys, address encryption keys, enrollment and routine pilot scope.
It retains the original policy in a protected backup volume, restarts only the
API and rolls back on failed TLS/authentication route verification. The route
requires the original mobile account bearer; staff credentials do not replace
that bearer. The verifier trusts TLS assertions only from loopback gateway.

The companion Android app now queues actual message and notification-open
callbacks, retaining original scope, event IDs and request bodies across retry.
The restricted server grant profile includes the execution identity columns
and observation conflict-target reads needed by receipt ingestion. Isolated
PostgreSQL grant regressions reproduced and verified those omissions.

The owner explicitly approved at most two receipt-test sends to the same pilot
device and opening their notifications through ADB. `test-receipts.py 1|2`
created campaigns 4 and 5, respectively, each with one provider attempt. Its
private per-test records prevent repeating the same test number. No additional
send is authorized by that script's existence. Receipt ingress does not change
the standing one-device PILOT approval or its manual pause procedure above.


Live acceptance on 2026-11-12: campaign 5's real notification tap persisted
OPENED and its user-1 read row. Campaign 4's original queued DELIVERED eventually
recovered after the narrow grant repairs. The owner separately approved exactly
one additional send (`test-receipts.py 3`, campaign 6); the unchanged final app
reported DELIVERED successfully. Each campaign has exactly one provider attempt.
No device receipt was synthesized from provider acceptance. The app's development
reload storage-lock race was reproduced in a focused regression and fixed by
sharing the lock across module instances. Ten receipt tests, app lint/typecheck,
arm64 build and isolated grant fixtures passed. Background/process-death results are recorded in the next section; independent
JS runtimes and VM reboot recovery remain unverified.
All services remained running, no PENDING intents remained, and runtime/policy
still matched the same one-device routine PILOT after the tests.

## Background and recent-apps dismissal acceptance — 2026-11-12

The owner accepted the proposed two-scenario Android test. Bounded test numbers
4 and 5 created campaigns 7 and 8, respectively, targeting only the existing
approved phone. No provider retry or additional binding was introduced.

- Campaign 7: Home put the app in background. DELIVERED existed before tapping;
  tapping the visible notification produced OPENED and one user-1 read row.
- Campaign 8: the staging app's recent-apps card was dismissed; `am kill`
  terminated its remaining background process. Both no process and no visible
  recent task were verified before sending. The notification appeared while
  the app was absent; tapping launched the app and persisted OPENED plus one
  user-1 read row. DELIVERED was not recorded in this scenario.

Each campaign has one provider attempt; each observed event and each read row
has count one, including overlapping Firebase/Expo tap handling. An OPENED
observation is genuine device evidence even when the OS did not execute a
DELIVERED callback. Provider SENT or notification shade visibility must never
be synthesized into a DELIVERED fact. This tested recent-apps dismissal and
background process death; Settings Force stop, reboot, offline retry and session
expiry remain separate scenarios. No application source changed during these
two tests. The existing routine single-device PILOT remains active.

## Expired-session and API-network outage acceptance — 2026-11-12

The owner requested the proposed offline and expired-session recovery scenarios.
No application source changed during these tests.

- Campaign 9 (bounded test 6) was accepted by FCM once but produced no observed
  device callback during this test. It remains SENT and is not success evidence.
- With the foreground app confirmed ready, campaign 10 (test 8) reached the
  device with the previous ten-minute mobile session expired. The API returned
  HTTP 401; the app displayed "renew the original mobile session. Pending: 1."
  No observation existed before renewal. A fresh pairing for the same account
  refreshed the existing installation and flushed the stored DELIVERED receipt.
- Campaign 11 (test 7) exercised an actual phone-to-API network timeout with a
  valid mobile session. `phone-api-outage.py` temporarily dropped inbound TCP
  8443 only from the verified phone source 10.1.19.199 to staging 10.1.16.109.
  FCM, ADB, SSH and other clients remained outside the rule. The device received
  the push but displayed "offline; queued for retry. Pending: 1." The database
  still showed SENT without an observation while the path was blocked.
  Removing the rule allowed automatic retry to persist DELIVERED, without
  pressing Retry receipts or sending another push.

Campaigns 10 and 11 each have one provider attempt, one receipt-request row and
one DELIVERED observation. No provider acceptance was substituted for a device
receipt. This isolates loss of the staging API path; complete loss of all phone
connectivity and a reboot while queued are distinct untested scenarios.

The outage helper uses only an ephemeral NET_ADMIN container with host-network
access and read-only host binaries. It arms an independent 45-second cleanup
before inserting the exact phone/port/comment rule. The local test also restored
that exact rule in `finally`; its absence was verified afterward. It never
changes persistent UFW policy. The helper is pinned to the verified test phone
address; inspect the current device address before any future invocation.
Status and watchdog records are in `~/hello-staging-provision/phone-api-outage*`.
The original one-device routine PILOT remains enabled.

## Repeatable deployment, backups and monitoring — 2026-11-12

The [operations runbook](OPERATIONS.md) contains the installed one-command
controller, rollback/recovery commands, schedules, key custody and limits.
Workstation entry point: `python3 deploy/staging/staging.py`.

Live acceptance completed against schema 41 (clean):

- Built all six hardened images from committed revision
  `90749a9c2c98b142c824d2e82e0489dc04486fce`; all existing image gates passed,
  including rejection of group-writable server/bootstrap policies. The Go source,
  module files and Dockerfile are unchanged from the previously running revision;
  this validates deployment mechanics rather than a functional feature upgrade.
- Installed dedicated management mTLS on four loopback ports. All role readiness
  probes passed, and an unauthenticated TLS client was rejected for lacking its
  client certificate.
- Deployed `git-90749a9-staging-ops`, rolled back to the retained original
  containers, then redeployed the checked immutable images. Each transition
  restored the original routine PILOT state. Eleven focused safety regressions
  passed, including rename/readiness/reopen/journal failures and diagnostics
  recovery failure handling.
- The controlled health drill detected a stopped API while maintenance was held,
  restarted it and verified all roles ready with no alerts. Cron subsequently
  produced fresh minutely health snapshots. The cron daemon is boot-enabled.
- Encrypted DB/config backups were created and verified on the Mac. A fresh
  networkless, sealed restore compared all 46 table counts/row digests and seven
  archived DB/config members in 4.142 seconds. No restored instance was activated.
  A tampered encrypted backup was rejected. Hourly VM backups run at minute 07.
- Existing role policy/credential fields matched protected pre-monitoring
  originals after removing only the added monitoring object. Source registration
  hashes matched the pre-deployment snapshot. Administrator login, API key checks,
  refresh rotation, logout and access revocation passed without sending a push.

Final state: the new release is committed, rollback is available, all four roles
are ready and the one-device routine PILOT remains active. No schema migration,
source replacement, consumer reset or health-test notification was performed.
Full hosted CI was not asserted: this workstation's GitHub CLI is unauthenticated.
Alerts currently stay in private VM status/syslog; there is no external receiver.
Scheduled backups remain on the VM until explicitly fetched; the tested Mac
copies are off-host, but automatic independent-storage delivery is not configured.
