# OneSignal → FCM cutover readiness

Updated 2026-11-09 (Asia/Jakarta). **Production cutover is on hold.**
Design: [ADR-06](../adr/adr-06-onesignal-fcm-cutover.md).

React Native, Android+iOS, current OneSignal use and the FCM destination have been confirmed.
The user believes a Firebase project probably already exists, but its identity,
access, apps/environments and readiness have not been verified. An empty checklist
means evidence is missing, not that the user's system necessarily has a problem.

## Evidence register

| Gate | Evidence to record | Role owner | Status |
| --- | --- | --- | --- |
| C01 Firebase inventory | Project/app/package/bundle/environment IDs, IAM owner, quota and APNs setup; evidence location without secrets | Mobile + ops | Firebase staging project ID `push-notifications-stagi-a6a41` provided in the local handoff on 2026-11-09. User reports signed Android/iOS test builds are available. Android package/iOS bundle IDs, IAM access, quota, APNs setup and build locations remain unverified. |
| C02 React Native compatibility | RN/package/native versions, supported address types and enrollment callbacks | Mobile | [Source revision inspected](mobile-source-checkpoint.md); release builds and callbacks pending |
| C03 Legacy behavior | Whether production sends/scheduled automation exist, operators and cohort exclusion/pause | Product + ops | Not verified |
| C04 Registry/consent census | User/device/installation counts, owner/epoch, unknown legacy, permissions/opt-out and activity | Backend + mobile | Scoped, read-only census available. Target counts, permission state, legacy-only devices and exact physical population require independent evidence. |
| C05 Modern enrollment | Auth, binding proof/version, refresh/logout/transfer, typed identity and recovery | Backend + mobile + security | Backend implemented and locally exercised; first client integration pending |
| C06 Delivery reliability | Atomic intent/outbox, budget/TTL, lease, safe offset, fault/replay evidence | Backend | Local PostgreSQL, Kafka and synthetic provider drills pass; target evidence pending |
| C07 Receipt/events | Device capability, early/late receipt, stats units, event v2 and consumer compatibility | Backend + mobile + consumer owner | Local receipt/statistics and reference-consumer fixtures pass; real reader acceptance pending |
| C08 Isolation | No identical business campaign reaches overlapping cohorts across providers | Product + ops | Backend route grants, held and retired generations and final pre-send rechecks pass locally. Pilot membership and broadcast approval have separate controls. External OneSignal exclusion remains unverified. |
| C09 Real devices | Android/iOS release builds: permissions, foreground/background/terminated behavior, tap/deep-link, offline and upgrade | Mobile + QA | Signed test builds are reported available. In the isolated local smoke on 2026-11-10, the user-authorized Android test app showed foreground receipts (campaigns 1 and 3), and the user confirmed the background system notification (campaign 4) and tap-to-open. Campaign 5's process-absent notification was cleared before tapping. Campaigns 6/7 exposed Metro and cold-start UI issues; after Metro forwarding and an Expo response fallback were fixed, campaign 8 displayed its `Last push` title/body after a process-absent send. Campaign 2's old FID returned `unregistered` and was retired. This is one local app/device only, not signed staging evidence; force-stop, deep-link routing beyond opening the app, offline/upgrade, iOS, independent ownership evidence and staging acceptance remain open. |
| C10 Capacity/observability | Agreed numeric SLO, rate/quota, load/soak, dashboards, tested alerts and unknown-outcome accounting | Backend + ops | Project owner approved the five-minute per-20k objective. Two isolated 20k runs completed in 49.424 s and 51.450 s. Five-campaign/100k synthetic bursts completed in 360.177 s at 400/s and 222.242 s at 1,000/s, with all Kafka IDs verified. These runs are local calibration only. Platform sign-off, target quota/headroom, approved burst profile, staging SLIs, receivers and soak remain open. |
| C11 Security/retention | Auth/ownership, key/installation secret lifecycle, logs, limits, deletion and backup/restore | Security + ops | Protected sessions, ingress, scoped retention, erasure impact preview and sealed restore have local evidence. Owner approval and erasure/retention gates remain. |
| C12 Stage rollback | Hold/drain/reconcile/compatible-backend rollback, client capability and restore drill | Backend + mobile + ops | Per-binding holds, project rollback, stale-grant rejection and isolated sealed restore exercised. Full deployment rollback and mobile compatibility remain open. |
| C13 Ramp decision | Cohort, count, dwell time, stop threshold, sign-off record and observation window | Product + mobile + ops | Backend accepts a separately reviewed cohort grant; named owners, thresholds and observation window remain open. |
| C14 Full coverage | Full eligible-audience gate or explicit accepted re-scope/straggler policy | Product | Backend requires a separately reviewed full-coverage grant. Target denominator, policy and approval remain open. |
| C15 Retirement | Legacy jobs disabled, SDK-free release verified, dependencies audited and credential/data cleanup sequenced | Mobile + ops | Not performed |

Actual owner names, dates, artifact/report links and results are added when
available. Secrets/keys/credential files must not be included in the register.

## Execution sequence after implementation and staging are ready

- [ ] Inventory Firebase/RN/legacy and define target/stop thresholds.
- [ ] Implement and verify ADR-01–05 and eligibility/hold controls.
- [ ] Verify consumer/read API schemas before enabling event v2.
- [ ] Release/test modern enrollment without enabling production FCM delivery.
- [ ] Demonstrate coexistence or choose a controlled maintenance transition.
- [ ] Run a test-device campaign with recipient authorization and a clear cohort.
- [ ] Rehearse hold/recovery/rollback with uncertain provider outcomes.
- [ ] Enable an FCM-only pilot and check isolation from legacy senders.
- [ ] Evaluate reports by denominator, app version, rate and coverage.
- [ ] Increase the cohort gradually after thresholds and dwell time are met.
- [ ] Open full broadcast only after coverage/re-scope is agreed.
- [ ] Observe for the agreed window, then retire OneSignal with a dependency audit.

The recorded backend checks used isolated services and synthetic addresses.
A backend send gate cannot cancel push notifications already accepted by the provider.
Rolling back an app that has removed the old SDK requires a compatible FCM path
or a new mobile release; merely changing an environment variable is insufficient.
