# OneSignal → FCM cutover readiness

Date: 2026-11-02. Status: design checklist, **not cutover authorization**.
Design: [ADR-06](../adr/adr-06-onesignal-fcm-cutover.md).

React Native, Android+iOS, current OneSignal use and the FCM destination have been confirmed.
The user believes a Firebase project probably already exists, but its identity,
access, apps/environments and readiness have not been verified. An empty checklist
means evidence is missing, not that the user's system necessarily has a problem.

## Evidence register

| Gate | Evidence to record | Role owner | Status |
| --- | --- | --- | --- |
| C01 Firebase inventory | Project/app/package/bundle/environment IDs, IAM owner, quota and APNs setup; evidence location without secrets | Mobile + ops | Not verified |
| C02 React Native compatibility | RN/package/native versions, supported address types and enrollment callbacks | Mobile | Framework confirmed; versions not yet confirmed |
| C03 Legacy behavior | Whether production sends/scheduled automation exist, operators and cohort exclusion/pause | Product + ops | Not verified |
| C04 Registry/consent census | User/device/installation counts, owner/epoch, unknown legacy, permissions/opt-out and activity | Backend + mobile | Approximate scale available; census not yet available |
| C05 Modern enrollment | Auth, binding proof/version, refresh/logout/transfer, typed identity and recovery | Backend + mobile + security | Proposed ADR-04; not implemented |
| C06 Delivery reliability | Atomic intent/outbox, budget/TTL, lease, safe offset, fault/replay evidence | Backend | Proposed ADR-01–03; not tested |
| C07 Receipt/events | Device capability, early/late receipt, stats units, event v2 and consumer compatibility | Backend + mobile + consumer owner | Proposed ADR-05; not tested |
| C08 Isolation | No identical business campaign reaches overlapping cohorts across providers | Product + ops | Not demonstrated |
| C09 Real devices | Android/iOS release builds: permissions, foreground/background/terminated behavior, tap/deep-link, offline and upgrade | Mobile + QA | Not tested |
| C10 Capacity/observability | Agreed numeric SLO, rate/quota, load/soak, dashboards, tested alerts and unknown-outcome accounting | Backend + ops | Not defined/tested |
| C11 Security/retention | Auth/ownership, key/installation secret lifecycle, logs, limits, deletion and backup/restore | Security + ops | ADR-10/11 not yet written |
| C12 Stage rollback | Hold/drain/reconcile/compatible-backend rollback, client capability and restore drill | Backend + mobile + ops | Not tested |
| C13 Ramp decision | Cohort, count, dwell time, stop threshold, sign-off record and observation window | Product + mobile + ops | Not defined |
| C14 Full coverage | Full eligible-audience gate or explicit accepted re-scope/straggler policy | Product | Not decided |
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

No live sends or account/provider changes occurred during this writing session.
A backend send gate cannot cancel push notifications already accepted by the provider.
Rolling back an app that has removed the old SDK requires a compatible FCM path
or a new mobile release; merely changing an environment variable is insufficient.
