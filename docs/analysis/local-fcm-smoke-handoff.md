# Local Android FCM smoke handoff

Updated 2026-11-10. Continue this work in English; the user asked not to receive
Russian-language messages.

## Roadmap position

The first unchecked roadmap item remains **Step 05**. The owner-approved initial
targets are recorded in `production-readiness-plan.md`, but quota/headroom,
failure scenarios, operational ownership, feasibility and target-environment
evidence are still open. This run adds local evidence for the **Step 40 Android
device gate**; it does not complete Step 40 staging acceptance or production
readiness. No iOS device or isolated staging backend was used.

## Completed in this local smoke

- Used only the synthetic `hello_fcm_smoke` database and the owner-authorized
  Android test app `com.example.hellofcmsmoke`.
- Opened the runtime only in that database after the guarded local preflight.
- Applied separate restricted-login approvals for its one Android binding
  (`FCM_ACTIVE`) and one-recipient `PILOT` cohort.
- Started a local API on `127.0.0.1:8082`, provisioned a synthetic admin, gave
  it access only to the smoke app, and created/sent campaign `1` through the API.
- The API returned `202 QUEUED`. The scheduler prepared and published it. The
  delivery worker made exactly one FCM attempt; PostgreSQL records campaign
  `COMPLETED`, intent `SENT`, `attempt_count=1`, and both outboxes empty.
- The Android screen showed **“Real FCM message received while the app was
  open”** and the expected title/body. A screenshot is at
  `/private/tmp/hello-fcm-smoke.1Ib7I1/local/device-screen.png`.
- Firebase project IDs match between the user-provided service account and
  Android config. The worker route matches the protected mobile policy.

**Do not resend campaign 1.** Its one-attempt test quota is consumed. The FCM
provider acceptance and Android foreground receipt are confirmed for this one
device; no delivery claim is made for other Android states or iOS.

## Admission checkpoint diagnosis and resolution

The admission-only worker initially restarted because its new Kafka consumer
group had no committed offset. The worker intentionally rejects Kafka's
negative `FirstOffset` sentinel. After verifying the isolated one-partition
topic, the local owner dry-ran and applied offset 0 for group
`hello-fcm-smoke-admission-v1`. It consumed the single send record and now
reports current offset 1, log end 1, lag 0, with an active member. No dead-letter
row was created, no provider attempt was added, and the campaign stayed
`COMPLETED` with one `SENT` attempt. The verified group-initialization process
is now documented in `docs/runbooks/delivery-worker.md`.

The scheduler and delivery-only worker were healthy at the last check. The
three container names are `hello-fcm-smoke-scheduler`,
`hello-fcm-smoke-admission`, and `hello-fcm-smoke-delivery`. The connected
Android device is still running the smoke app. The app's reverse API tunnel
continues to target port 8080; the campaign API used port 8082.

## Continue safely

1. Inspect the admission group `hello-fcm-smoke-admission-v1`, Kafka broker
   logs, and the worker's current state. It was stable at offset 1 / lag 0 at
   handoff. Do not create another campaign or alter the completed intent.
2. Stop the three local daemons when no more smoke work is planned. The local
   database remains unsealed with dispatch mode `PILOT`; never point these
   policies or containers at production.
3. Keep the user-provided service account and generated policy files in their
   protected locations. Do not print or commit them.
4. Update the Step 40 evidence register only with the limits above. Step 05
   remains the roadmap's first unchecked item.
5. The project owner shared a Cloud Console screenshot filtered to FCM showing
   a “Send requests per minute” quota of 600,000 and current usage of 0. I
   recommend an initial staging cap of 60,000 sends/minute for this backend,
   reserving 90% for other project use and unexpected peaks; this is not yet
   owner/operations-approved or provider-validated. The sender service account
   itself still cannot read quotas (`serviceusage.quotas.get` returned 403); no
   quota settings were changed. Recent peak usage, campaign TTL, soak/burst
   profile, operational owners and target acceptance remain open.

## Local files and working tree

The isolated temporary workspace is `/private/tmp/hello-fcm-smoke.1Ib7I1`.
It contains generated role passwords, synthetic staff login/access token,
daemon policies, review artifacts, TLS test material and the screenshot. Treat
all of it as private; the JSON policies contain DB credentials and key material.
The user-provided sender credential remains under
`/Users/koperasiastra/.config/hello/staging/`; never copy its contents into chat
or the repository.

The smoke setup and initial handoff were committed in `8367ac0`
(`chore(local-fcm): add isolated smoke setup and handoff`); roadmap/ADR status
corrections were committed in `711773c`; exact-origin CORS support for M46 is
committed in `8d2bc94`. Production portal origin and browser acceptance remain
open. Review `git status` and the latest commits before continuing. The current
Codex session could not inspect the
Docker or ADB sockets because the execution environment denied access. The last
confirmed state was the three smoke containers healthy and the Android device
connected as described above; do not assume that state is unchanged.

`go vet` and focused race tests passed earlier in this task; `govulncheck`
remained blocked by network/DNS access. The M46 CORS increment passed focused
tests for middleware, configuration, ingress and server plus `go vet` for those
packages. No additional tests were run during the roadmap/ADR documentation
refresh.
