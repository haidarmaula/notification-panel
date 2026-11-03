# ADR-06 — React Native migration, FCM enrollment and reversible cutover

Application-scope amendment (2026-11-03): [ADR-12](adr-12-multi-application-isolation.md)
requires explicit app scope and per-app policy across this design. kaiapps2 is
one consumer; existing unscoped examples are not a modern cross-app contract.
This is proposed design, not implemented isolation; see CR10 in the
[consistency register](../analysis/adr-01-11-consistency-review.md).

**Status:** proposed, 2026-11-02. Design only; not implemented, accepted or
executed. NEW companion SPEC: `docs/specs/adr-06-onesignal-fcm-cutover.md`
(not yet written). Operational evidence checklist:
[cutover readiness](../analysis/cutover-readiness.md).

**Scope:** this backend's migration controls, enrollment/routing eligibility,
coverage API, payload contract, release/runbook/testing artifacts and data
migrations; coordinated work in the separate React Native repository and
provider consoles. No mobile code or external settings are changed here.


Cross-ADR protocol: [consistency review](../analysis/adr-01-11-consistency-review.md).
Its shared lock/identity rules and verification gates apply to this proposal;
review completion does not mean acceptance or runtime implementation.

## Confirmed requirements and evidence limits

- User confirms React Native, Android+iOS, OneSignal currently used, FCM as
  the replacement, and iOS APNs through Firebase. Approximately 20,000 active
  devices, five campaigns/month, largest broadcast all active users.
- User expects a Firebase project probably already exists through OneSignal's
  FCM setup. Its identity/access/configuration are **unverified**. Prefer
  inventory/reuse of a suitable existing project; never create a new project
  merely because this backend has not been configured yet.
- No React Native source/package manifest, live device registry, Firebase or
  OneSignal console has been inspected. Searching this checkout for *.dart,
  *.swift, *.kt, package.json, pubspec.yaml, Gradle and Xcode project files
  found no mobile source. React Native version, native/library versions, FID
  support, app/bundle IDs, permission/consent behavior and actual stored address
  types remain unknown.
- Current worker constructs OneSignal. There is no migration controller,
  cohort registry or runbook in runtime. ADR-01–05 describe future behavior.

## Dependencies

[ADR-01](adr-01-provider-retry.md): complete intent snapshot, per-device budgets,
lease/fencing and recovery. [ADR-02](adr-02-worker-consumption.md): safe admission
and replay. [ADR-03](adr-03-transactional-outbox.md): atomic acceptance/outbox.
[ADR-04](adr-04-provider-selection.md): typed immutable registration and explicit
FCM adapter. [ADR-05](adr-05-receipt-ingestion.md): scoped receipt evidence,
versioned statistics/events. NEW ADR-07/08/10/11 supply topology, observability,
retention and security. Cutover cannot precede their required implementation
and staging evidence merely because this ADR is written.

## A-1. Inventory and identifiers before any production switch

### Context

OneSignal subscription IDs, provider tokens and FIDs are different identities.
An existing project does not establish that a stored string is usable by the
new sender or that the backend has permission to use that project.

### Decision

Inspect React Native dependencies and native Android/iOS configuration with
the mobile owner. Inventory existing Firebase project numbers/IDs, app IDs,
package/bundle/environment identifiers, Android sender configuration, iOS APNs
configuration, sender IAM, quota and operators. Record identifiers and evidence
locations, never copy private keys/service-account JSON into docs or chat.

Inventory registry counts by platform, source, installation attribution,
address type, user ownership, app version, activity and duplicate/conflict
state. Treat old provider labels as unverified metadata. No string-length or
UUID heuristic upgrades a row into FCM readiness. No automatic conversion of
OneSignal player/subscription IDs to Firebase destinations.

Modern clients re-enroll their actual Firebase-issued destination under
ADR-04's authenticated typed contract. Prefer the existing correct Firebase
project after verification; if project/sender changes are necessary, make
new enrollment explicit. Reuse of an old provider token is allowed only after
proven provenance, project/app compatibility and current binding ownership,
not a blind database relabel/backfill. Do not assume OneSignal can export all
usable addresses or FIDs; re-enrollment is the baseline migration mechanism.

### Alternatives rejected

Global provider replacement and bulk APNS/FCM relabeling can misroute an entire
broadcast. Creating an unneeded Firebase project can break sender alignment
and force avoidable re-registration. An assumed export is not a migration plan.

### Consequences

Firebase/RN evidence gates the companion SPEC's final address mode and rollout
configuration. Existing SDK support for Fid in Go alone cannot select the
React Native client API. Unknown legacy rows remain visible, not discarded.

## A-2. Mobile release sequence and enrollment evidence

### Context

Backend availability cannot update an installed mobile app. Removing the old
SDK before registration/payload/receipt support is deployed can create a gap.

### Decision

Use staged releases rather than requiring every device to update at once:

| Stage | Backend readiness | React Native behavior / gate |
| --- | --- | --- |
| M0 inventory | Existing behavior retained; no new sends | Inspect actual packages/native setup, permission and old subscription behavior |
| M1 prepare | Modern registration, auth, retained identities and receipt route available but production FCM dispatch disabled | Test Firebase initialization, enrollment, callbacks, protected installation storage and payload parser |
| M2 enroll/pilot | Cohort eligibility, rate/lease/outbox and monitoring implemented | Eligible versions register Firebase address, support signed per-device receipts and durable offline reporting; pilot devices only |
| M3 expand | Evidence-based cohort activation | Grow compatible installations; retain required legacy handling only for the agreed transition window |
| M4 retire | Accepted coverage and rollback/retirement gates | Release without OneSignal SDK/config/callbacks and verify platform behavior again |

A transitional app may retain OneSignal while enrolling Firebase **only if**
coexistence is tested on real Android/iOS builds. Do not assume delegates,
notification services/background handlers, Firebase project configuration or
permission handling coexist automatically. If they do not, use a controlled
version/cohort maintenance transition with explicit business impact rather
than ship an untested bridge. Do not select a particular RN Firebase library
without inspecting the existing package versions and compatibility.

The modern client handles enrollment on login/startup, address refresh and
app upgrade; proves installation ownership on refresh/transfer; and queues
retryable receipts securely within ADR-05's lifetime. Logout advances owner
epoch/deactivates the binding and clears previous-user pending reports.
Same-owner address refresh preserves valid receipt reporting for old pushes.
Permission/consent handling must remain explicit; FCM setup must not silently
reset OneSignal opt-out or re-enable a user who opted out. Inventory that
behavior before declaring all active devices eligible.

Enrollment evidence separates authenticated registration accepted, compatible
app version, current owner/epoch, permission/consent eligibility and observed
test delivery. Self-reported metadata is not proof of receipt. Record last
registration time; do not call every historical row an active installation.

### Alternatives rejected

One backend flag cannot upgrade clients. Removing all OneSignal code in the
first preparatory build destroys a possible rollback path before FCM works.
Keeping both senders permanently contradicts the confirmed end-state.

### Consequences

Mobile work is a coordinated dependency in its own repo, not UI work in this
backend. RN version/platform matrix and actual SDK method determine FID versus
explicit registration-token compatibility cohorts. No mobile release executed.

## A-3. One active delivery route per installation and campaign

### Context

Application-level dedup cannot reliably prevent OS-displayed duplicates if
both providers deliver the same notification independently.

### Decision

NEW migration state records installation eligibility, cohort, route_epoch,
active path and activation audit independently of the immutable registration
binding. States: LEGACY_ONLY/UNKNOWN, FCM_ENROLLED, FCM_ACTIVE, HELD and RETIRED.
These are migration-control states, **not** new notifications.status values.
Only FCM_ACTIVE current bindings may enter new production FCM intent snapshots.
FCM_ENROLLED is readiness metadata, not permission to send.

Activation/hold uses authenticated operator control with expected revision,
audit, reason and immutable evidence reference; exact CLI/admin surface is NEW
and belongs in SPEC. No direct arbitrary SQL or request body selects provider.
Global FCM dispatch enable plus per-installation route are checked before
reservation and immediately before external submission. Disabled/held routes
defer without starting new calls; already-reserved uncertain attempts retain
their consumed budget. Deadline continues to apply during a hold.

Snapshot installation/binding/owner/route identity in the intent. Changing a
route never rewrites the audience, address, owner, deadline or attempt history
of existing intent. Resuming held pending work requires explicit revalidation
and a current execution-authorization epoch separate from the immutable audience
snapshot epoch; no automatic resurrection after FAILED/expiry/owner change.
Already accepted/in-flight provider calls cannot be recalled by a DB flag.

During transition, the new worker remains FCM-only. Any legacy sender is a
separately controlled old deployment/service, not automatic fallback in the
same processor. If legacy campaigns exist, use one authoritative campaign
schedule/register and prove cohort exclusion or pause legacy automation for
the overlapping campaign window. A new DB flag cannot control an external
OneSignal dashboard job. If install-level mapping/exclusion is unavailable,
pause overlapping production legacy campaigns during the pilot/transition;
do not claim software dedup across uncoordinated senders.

### Alternatives rejected

Dual-send to real users tests the duplicate-notification failure mode, not
safe parity. Device format guessing and automatic error-based provider failover
violate immutable attribution and can resend an uncertain accepted push.

### Consequences

Migration control is a NEW dependency of ADR-01 eligibility/recovery and must
be specified before dispatch enablement. Historical and uncertain legacy sends
need reconciliation; deterministic Kafka keys alone cannot dedup them.

## A-4. Coverage and full-broadcast admission

### Context

A successful broadcast to only enrolled devices is not a successful migration
of all active users. A user may have multiple devices, no current device, or
legacy registrations that cannot yet be matched to an installation.

### Decision

Expose a backend migration/coverage report (NEW read-only operator/API surface)
with timestamp, filter/cohort definition and counts by platform/app version:
active users, resolved installations, verified modern bindings, FCM_ACTIVE,
legacy-only, unknown/unattributed, permission/consent-excluded and held. Include
device/user denominators and reconciliation quality, never raw addresses or
secrets. Approximately 20,000 is a planning estimate, not an exact census.

Pilot sends explicitly target their limited cohort and record exclusions.
Production full broadcast requires a reviewed coverage gate against the
eligible active-user scope, compatible mobile adoption and documented treatment
of stragglers. No invented adoption percentage. If the requirement remains all
eligible active installations, legacy/unknown/held eligible entries block
full-broadcast enablement until migrated or explicitly re-scoped by the owner.
Do not change the meaning of full broadcast to all enrolled devices silently.

Before **new immediate API acceptance** validate coverage readiness under the
campaign transaction; not ready returns a mapped domain 409 (proposed
audience_not_ready) without key/outbox/status commit. Recheck in the complete
fan-out transaction. Scheduled promotion uses ADR-03's durable queue/deadline
and visible preparation hold, not a nonexistent HTTP response. If a
coverage gate becomes invalid between acceptance and preparation, persist a
visible preparation hold/reason and defer through ADR-01/02's guarded admission
retry/quarantine/recovery, without partial intent commit or speculative nil.
It must either become ready within its original deadline or reach explicit
FAILED/ttl_expired; never report partial audience as COMPLETED. Native device
deactivation/consent exclusion follows the defined eligibility policy; it is
not conflated with missing migration coverage.

### Alternatives rejected

Counting only newly enrolled devices hides users lost during migration. A live
dashboard denominator is not the campaign snapshot. Blind backfill cannot
reconstruct missing installation/owner/consent evidence.

### Consequences

Coverage API/error is a new contract to document in SPEC/API docs. Admission
holds require persisted reason/deferral and alerts, not a hot loop; numeric
SLO and allowed straggler policy remain owner decisions before production.

## A-5. Safe comparison, real-device pilot and staged ramp

### Context

Comparing two providers' dashboard counters with different definitions cannot
establish equal reach or exactly-once. Production user traffic is not a load fixture.

### Decision

Comparison has three separate evidence classes:
1. Offline audience/payload comparison, without send side effects: resolved
   cohorts, exclusions, owner isolation, schemas, deep-link/tap contract and
   notification IDs. Use sanitized fixtures, never export secret addresses.
2. Authorized isolated test-device campaigns: Android/iOS, foreground/background/
   terminated where supported, permission states, upgrade, account switch,
   refresh, offline/reconnect and tap/deep-link. If both transports are tested,
   use clearly labeled separate test campaigns/identities, not duplicate sends
   to unsuspecting production users.
3. FCM-only pilot and gradual cohort expansion, supported by ADR-08 metrics and
   ADR-01–05 fault/load evidence. Candidate expansion stages are internal test,
   small opt-in cohort, progressively larger cohorts, then full eligible scope.
   Counts, dwell time and stop thresholds are set before execution; none is
   inferred from the five-campaign monthly business schedule.

Measure durable queue-to-submission distributions, actual provider request
rate/latency/errors, retry/DLQ/outbox lag, stuck/expired work, duplicate reports,
client observation coverage and user/device isolation failures. Keep provider
acceptance, client observation and visible display as distinct results. Never
use low receipt count alone as proof of provider loss; investigate reporting
coverage and platform restrictions.

Load/soak 20,000-device scenarios against simulators first; real provider tests
are bounded to authorized recipients and quota. Use controlled rate ramp and
pre-agreed headroom. The proposed <=60s healthy broadcast benchmark remains an
experiment, not a cutover guarantee or device arrival promise.

### Alternatives rejected

An unobserved production broadcast is not a pilot. Matching aggregate dashboard
percentages with different semantics is not parity. One tiny dry-run cannot
establish real-device behavior or sustained recovery capacity.

### Consequences

Each pilot report identifies app/backend versions, project/environment/cohort,
route revision, denominator, rate profile, observation window and limitations.
Missing numeric acceptance thresholds block promotion, not continued design.

## A-6. Rollback by migration stage, preserving durable work

### Context

Backend code can often roll back independently of app-store adoption. A device
with the OneSignal SDK removed is not restored by changing PUSH_PROVIDER.

### Decision

First response to a rollout fault is a scoped/global **dispatch hold** and stop
cohort expansion; preserve registrations, intents, budgets, deadlines, outboxes,
receipt keys and evidence. In-flight calls may still complete; continue guarded
result/receipt recording. Admission policy must expose the hold and avoid
unbounded acceptance during a prolonged outage. Resume only reviewed eligible
work with original limits; no reset or replay of terminal campaigns.

| Stage / fault | Supported response | Explicit limit |
| --- | --- | --- |
| Enrollment only, FCM not enabled | Hold enrollment activation; retain/revert compatible backend; legacy remains only if actually operational | Existing legacy reach/config is unverified until inventoried |
| FCM pilot, bridge clients verified | Hold FCM, reconcile per-device accepted/unknown/unsubmitted work; use reviewed legacy cohorts only if still valid | Never blindly resend accepted or uncertain FCM work through OneSignal |
| FCM active, clients still have tested legacy capability | Restore a reviewed exclusive route only for proven compatible installations after hold/drain/reconciliation | No assumption that an old OneSignal subscription remains enabled/valid |
| OneSignal removed from installed build | Hold or roll back to a compatible **FCM** backend/config; mobile fix is a new store release | Backend flag cannot restore removed SDK or instantly downgrade installed apps |
| Schema/pending-work problem | Disable affected execution, retain compatible schema, restore tested backup or roll forward under runbook | Down migration that drops intent/outbox/evidence is not safe rollback |

Provider-accepted-before-DB-write uncertainty remains explicit. Reconciliation
may require manual classification and a business choice between a possible
duplicate and omission; do not claim a perfect transparent fallback. If a
legacy service is unavailable or no bridge was proven, fallback is hold/fix
forward, not an imaginary second provider.

### Alternatives rejected

Automatic OneSignal failover breaks the selected FCM-only worker contract and
can duplicate uncertain pushes. Resetting budget/deadline or restoring a DB
snapshot without accounting for external sends can repeat historical work.

### Consequences

Rollback drills include credential/config changes, worker restart and client
compatibility, not just git revert. Owners must agree the observation/legacy
retention window before pilot; no arbitrary N-day promise is adopted here.

## A-7. Go/no-go, retirement and ownership

### Context

Retiring OneSignal changes the available rollback path and may strand old app
versions. SDK removal alone does not retire campaigns, credentials or data.

### Decision

Go/no-go needs evidence for identity/consent coverage, RN platform behavior,
backend correctness/security, consumer schema compatibility, capacity, alerting,
backup/restore and stage-appropriate rollback. Record operator, product/mobile/
backend/security owners, outcome, unresolved risks, cohort and thresholds. See
the checklist; writing this ADR or checking a development task is not approval
to send to 20,000 devices or change provider consoles.

Retire after accepted adoption/straggler handling and observation window:
disable legacy automated campaigns/senders, verify no scheduled jobs or old
workers can still dispatch, ship/test SDK-free clients, stop legacy enrollment,
revoke backend/provider credentials under the agreed sequence, and archive or
delete legacy data per ADR-10/11. Stop exporting/synchronizing data to OneSignal.
Do not revoke keys/APNs/Firebase credentials blindly: inventory shared project,
app and credential dependencies first so FCM or another service is not broken.

Document the point after which OneSignal fallback is unavailable. End-state
uses explicit FCM and no permanent OneSignal runtime credentials/dependency.
Historical ONESIGNAL records remain labeled as history until lawful deletion.

### Alternatives rejected

Immediate deletion removes evidence and rollback options before confidence is
established. Indefinite legacy operation contradicts the agreed replacement
and increases operational complexity.

### Consequences

Dependencies outside this repo require coordinated execution. Product decides
stragglers/minimum app versions; mobile owns release/permission/display behavior;
backend owns delivery contracts; ops owns runtime/alerts/credentials/restore.
Actual names and dates remain unset rather than guessed.

## A-8. Implementation and remaining decisions

### Context

Migration controls and operator visibility do not exist yet in this checkout.

### Decision

NEW migration/query/repository/control surfaces implement eligibility, cohort,
route revision, hold reason, activation audit and coverage reporting. Follow
existing feature layout/auth conventions; use audit constants for new persisted
staff actions. Exact files/actions/indexes/migration numbers and read/operator
API shape are fixed in companion SPEC, not invented as existing symbols.
Migration state persists across restart; protected route changes require CAS
and idempotent operator commands. Admission and delivery share one authoritative
eligibility check; no separate cron or legacy endpoint can bypass it.

Before implementation acceptance establish RN/framework and package versions,
project/app/environment IDs and access owners, legacy operational use, identity/
consent mapping, FID/token compatibility, release plan/coexistence evidence,
numeric SLO/ramp/stop thresholds, straggler policy, consumer inventory and
key/data retention. Do not require raw credentials to answer these questions.

### Alternatives rejected

Config-only migration has no durable per-installation evidence or replayable
audit. A copy of this ADR is not an executable rollout/rollback runbook.

### Consequences

Step 11 is complete as a proposed design when this document/checklist is
reviewable. Actual migration stays gated by implementation and production
readiness steps 40–46. NEW ADR-07 is the next design task.

## Primary sources and verification boundary

Checked 2026-11-02:
[Firebase scaling](https://firebase.google.com/docs/cloud-messaging/scale-fcm),
[Apple setup](https://firebase.google.com/docs/cloud-messaging/ios/get-started),
[registration management](https://firebase.google.com/docs/cloud-messaging/manage-tokens),
[OneSignal mobile SDK reference](https://documentation.onesignal.com/docs/en/mobile-sdk-reference),
[React Native Firebase messaging](https://rnfirebase.io/messaging/usage).
RN Firebase is a candidate integration reference, not evidence that the user's
app uses that library/version. OneSignal exposes separate subscription and
provider-token concepts; this is not a guarantee of export/reusability.

No mobile code/console/credential read, live provider send, migration, benchmark
or deployment was executed. Cross-provider guarantees are deliberately bounded.

## Design verification cases for the companion SPEC

| Input / condition | Expected observable result |
| --- | --- |
| Existing Firebase project suspected but unknown | Inventory required; no automatic new project or claimed verified setup |
| OneSignal ID or ambiguous APNS/FCM row | No automatic conversion/activation; modern enrollment required |
| RN SDK lacks chosen address mode | Cohort not activated until tested compatible mode/release exists |
| Concurrent enrollment and account transfer | Immutable binding/owner epoch preserved; stale proof/version rejected |
| Legacy-only client after backend release | Visible not-ready coverage; no silent FCM dispatch or false reach count |
| Two senders could reach same cohort | Activation blocked until exclusive routing or campaign pause proven |
| Full broadcast with migration exclusions | Admission blocked or explicitly re-scoped; no silent partial success |
| Readiness changes after acceptance | Visible preparation hold/recovery within original deadline; no partial snapshot commit |
| FCM hold during in-flight request | No new eligible calls after gate check; in-flight uncertainty retained, not recalled |
| Resume pending work | Current owner/route revalidated; immutable audience, deadline and budget preserved |
| Receipt retry after same-owner refresh | Valid old payload observation accepted while owner_epoch matches |
| Logout/account switch then old report | Old epoch rejected, not credited to replacement owner |
| Pilot telemetry differs from legacy dashboard | Report metric definitions/coverage; no unsupported parity claim |
| Crash/rollback after uncertain FCM accept | Reconcile; no automatic cross-provider replay |
| SDK-free app needs rollback | Compatible FCM rollback or forward app fix, never imaginary OneSignal flag recovery |
| Legacy retirement | Dependency audit, accepted straggler policy, credential sequence and evidence recorded |
'''
