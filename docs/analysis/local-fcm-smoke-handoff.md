# Local Android FCM smoke handoff

Updated 2026-11-10. Continue this work.

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

**Do not replay or retry campaigns 1–8.** Campaigns 1 and 3 were `SENT` in the
foreground; campaign 4 was `SENT` with user-confirmed background notification
and tap-to-open; campaigns 5–8 each had one accepted provider attempt, while
campaign 2 failed as `unregistered`. Campaign 8’s title/body appeared in the
app’s `Last push` UI after its process-absent send and cold-start tap, using the
Expo response fallback. This is local evidence for one Android app/device only;
force-stop, iOS and staging behavior are not claimed.

## Admission checkpoint diagnosis and resolution

The admission-only worker initially restarted because its new Kafka consumer
group had no committed offset. The worker intentionally rejects Kafka's
negative `FirstOffset` sentinel. After verifying the isolated one-partition
topic, the local owner dry-ran and applied offset 0 for group
`hello-fcm-smoke-admission-v1`. It consumed the single send record and, at the
pre-stop check, reported current offset 1, log end 1, lag 0, with an active member.
No dead-letter
row was created, no provider attempt was added, and the campaign stayed
`COMPLETED` with one `SENT` attempt. The verified group-initialization process
is now documented in `docs/runbooks/delivery-worker.md`.

## Follow-up check (2026-11-10, 10:09 Asia/Jakarta)

The scheduler, admission and delivery smoke containers were still running. The
admission group remained at committed offset 1 / log end 1 / lag 0 with an
active consumer. The last ten minutes of broker and worker logs had no matching
warning, error, restart or FCM diagnostics.

Metro was already serving on port 8083 (`/status` returned
`packager-status:running`); its executable was Node 26.12.1, matching the app's
`.nvmrc`. The Android app rendered its JavaScript UI. The ADB reverse list
contained ports 8081 and 8083. A follow-up screenshot is at
`/private/tmp/hello-fcm-smoke.1Ib7I1/local/device-screen-followup.png`. The app
currently reports Firebase not registered and backend not enrolled; its backend
URL is still the default `8080` while the campaign API uses `8082`, and its
application UUID and one-use pairing code are blank. This confirms bundle
recovery only, not enrollment or another device receipt. No registration request
or FCM send was made. After this check, the three smoke containers were stopped
cleanly at 10:11 Asia/Jakarta with owner authorization; each exited 0. Kafka and
PostgreSQL remain healthy, while the API, issuer and Metro listeners remain up.

## Local FCM replacement and device-state checks (2026-11-10)

Campaign `2` targeted the existing synthetic user after the first app data loss.
Its single provider attempt failed with normalized reason `unregistered`; the
backend retired that old binding. The campaign is `FAILED` and must not be retried.

The same Android test app/user was then re-enrolled with a fresh loopback issuer
session. The restricted binding issuer/operator applied `FCM_ACTIVE` to the
existing one-recipient `PILOT` cohort, restoring exactly one active binding. Its
restricted review artifact is
`/private/tmp/hello-fcm-smoke.1Ib7I1/local/binding-replacement-review-98325e5b-3938-453d-8b92-75d1da98d417.json`.
Campaign `3` completed after one FCM `SENT` attempt; the foreground app displayed
the expected title/body. Campaign `4` completed after one attempt; the user
confirmed its background system notification and that tapping it opened the app.
Campaign `5` posted while the app process was absent and not force-stopped, but
the user cleared its notification before tapping.

Campaign `6` completed with one `SENT` attempt, but tapping it showed “Unable to
load script” because the installed app expected device port 8081 while Metro
served on host port 8083. ADB reverse now maps device `tcp:8081` to host `tcp:8083`;
Metro was restarted with Node 26.12.1 and the app UI loaded. Campaign `7` then
completed with one `SENT` attempt, but its tap left `Last push` blank. The local
test app at `~/projects/hello-fcm-smoke/App.tsx` now supplements RNFirebase's
initial/open callbacks with Expo's last-response lookup and response listener.
`expo lint` and `tsc --noEmit` passed after this change.

Campaign `8` completed with one `SENT` attempt after the app process was absent
and not force-stopped. After the notification launched the app, `Last push`
displayed the campaign title/body, verifying the cold-start response path. This
is one local Android device only; it does not verify force-stop behavior, iOS,
background operation of the production client or staging acceptance.

Both outboxes drained. Campaigns `1` and `3`–`8` are `COMPLETED` with one FCM
`SENT` attempt each; campaign `2` is `FAILED` with one `unregistered` attempt. No
campaign was retried. The final admission checkpoint is offset 8 / log end 8 /
lag 0 with no active member after the workers were stopped.

## Continue safely

1. After worker shutdown, the admission group `hello-fcm-smoke-admission-v1`
   has no active members; committed offset 8 / log end 8 / lag 0. Do not reset
   the checkpoint or replay or retry campaigns `1`–`8`.
2. The three dedicated smoke containers were stopped cleanly after campaign `8`
   completed. Kafka and PostgreSQL remain running; the local database remains
   unsealed with dispatch mode `PILOT`. Never point these policies or containers
   at production.
3. Keep the user-provided service account and generated policy files in their
   protected locations. Do not print or commit them.
4. Update the Step 40 evidence register only with the limits above. Step 05
   remains the roadmap's first unchecked item.
5. The project owner shared a Cloud Console screenshot filtered to FCM showing
   a “Send requests per minute” quota of 600,000 and current usage of 0. I
   recommend an initial staging cap of 60,000 sends/minute for this backend,
   reserving 90% for other project use and unexpected peaks; this is not yet
   owner/operations-approved or provider-validated. The existing worker policy
   supports a static project allocation with `project_requests_per_second` and
   `replica_count`; deployment must enforce that count and include other senders
   because there is no cross-process coordinator. The sender service account
   itself still cannot read quotas (`serviceusage.quotas.get` returned 403); no
   quota settings were changed. Recent peak usage, campaign TTL, soak/burst
   profile, operational owners and target acceptance remain open.
6. The earlier “Unable to load script” was a Metro port issue: the test app
   expected device `localhost:8081`, the campaign API is on `127.0.0.1:8082`, and
   host Metro now serves on port 8083 under Node 26.12.1. ADB forwards device
   `tcp:8081` to host `tcp:8083` for the bundle and `tcp:8082` to host `tcp:8082`
   for the API. The app's backend field resets to `8080` on a fresh JS start; set
   it to `8082` for this API instance. The temporary issuer ran on loopback
   `8084` for the replacement enrollment and was stopped; a future enrollment
   needs a fresh issuer session and matching device reverse. Do not kill the
   existing API, issuer or Expo processes without confirming ownership.
7. The replacement test app reports Firebase registered and backend enrolled.
   There is one active Android binding with an applied `FCM_ACTIVE` route in the
   same `PILOT` cohort; the previous unregistered binding remains retired. The
   user confirmed campaign `4` appeared as a background system notification and
   tapping it opened the app. Campaign `8` was sent with the process absent and
   not force-stopped; after the cold start, `Last push` showed its title/body.
   The test app now reads Expo's last notification response and listens for tap
   responses; `expo lint` and `tsc --noEmit` pass. Do not create another binding
   or route, or replay campaigns `1`–`8`.

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
corrections were committed in `711773c`; exact-origin CORS support for M46 was
committed in `8d2bc94` and its optional-policy compatibility fix in `bbd0cef`.
Production portal origin and browser acceptance remain open. The three smoke
workers are stopped after campaign `8`; the admission group is at offset 8 / log
end 8 / lag 0 with no active member. Kafka and PostgreSQL remain healthy, as do
the local API and Metro. The Android device is connected; ADB maps device port
8081 to Metro on host port 8083 and port 8082 to the local API. The replacement
test app reports registered/enrolled and one active binding remains. Campaigns
`1`, `3`, `4`, `5`, `6`, `7` and `8` are `COMPLETED` with one FCM `SENT` attempt
each; campaign `2` is `FAILED` (`unregistered`). Campaign `4` was user-confirmed
as a background notification and tap-to-open. Campaign `5` was cleared before
tapping; campaigns `6` and `7` had cold-start UI issues during Metro/response
handling. After the Expo response fallback was added, campaign `8` appeared in
`Last push` after the app process was absent. Both outboxes are empty. Do not
replay or retry campaigns `1`–`8`. Force-stop, iOS, and staging acceptance are not
claimed.

On 2026-11-10, `bash scripts/ci.sh format`, `modules`, `build`, `test` and `vet`
all passed; the test gate ran the full race-enabled Go suite. The focused
daemon/FCM/dispatch/observability tests and `bash scripts/check-monitoring.sh`
also passed. `bash scripts/ci.sh vuln` completed with no call-reachable findings,
but reported one vulnerable imported package and three vulnerable modules whose
vulnerable code was not called; do not describe the dependency graph as clean.
The Go binary is `/usr/local/go/bin/go` in this shell. The M46 CORS increment
passed focused tests for middleware, configuration, ingress and server. The local
test app's `App.tsx` now checks Expo's last notification response and subscribes
to notification-response events; `expo lint` and `tsc --noEmit` passed on Node
26.12.1, and campaign `8` verified the cold-start UI state. No backend source
files were changed.
