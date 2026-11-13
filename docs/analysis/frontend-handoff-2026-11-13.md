# Frontend implementation handoff — 2026-11-13

## Resume validation — 2026-11-13

The delayed-template regression is fixed without relaxing input guards. Final
local checks passed: 16 browser tests (including terminal-poll cessation), seven
frontend unit tests, build/typecheck, lint, formatting and bundle budget. Initial
JavaScript including modulepreloads measures 62.1 KiB gzip, not entry-only size.
The dashboard package passes 36 race tests; Go format, module verification,
build, vet and reachable vulnerability checks pass. The earlier full 1,510-test
race result below remains prior-session evidence, not a new full-suite run.
Template and membership mutation cache updates now retain their originating
route identity. Desktop settings and mobile logs were visually checked; the
isolated BFF browser settings screen had no console warnings or errors.
Commit, hosted CI authorization and staging verification follow this checkpoint.

## Objective and current state at handoff

User approved completing the dashboard UX expansion, including Segment under
Audience. Work is implemented in the current checkout on main but is **not
committed, pushed or deployed**. User requested a checkpoint because their
session quota is nearly exhausted. Continue from these edits; do not restart.

Read AGENTS.md. Maintained docs/comments are English; user communication is
Indonesian. Commit directly to main with scoped Conventional Commits, no PR or
branch. Preserve unrelated work. Read Go standards before Go changes. Relevant
frontend, incremental implementation, git and code-review skills were consulted;
the one pinned Go skill read was golang-security. References already consulted:
Anti-slop and Impeccable, with existing visual language in web/dashboard/DESIGN.md.

## Implemented changes

- Workspace: desktop sidebar hide/show with persisted preference; mobile Escape,
  focus return, focus containment and inert main content; expanded real routes.
- Light/Dark/System appearance in src/app/preferences.ts and semantic CSS
  overrides. Store only appearance/sidebar preferences, never private data.
- Header shows LOCAL on loopback and actual host/port elsewhere. It does not yet
  display a configured STAGING label or expose dispatch policy.
- New lazy management modules under web/dashboard/src/features/management/:
  Overview, Templates (create/edit/deactivate/use in new draft), Segments
  (manual CRUD and paginated membership add/remove), Audience (scoped users and
  device registration inspection), Settings (theme, read-only app identity,
  current staff name/email updates), shared pagination/write/unsaved helpers.
- Delivery logs route selects campaigns and links to per-campaign results.
  No invented aggregate log endpoint. Campaign list has exact ID lookup and URL
  cursor state, not global search/status filtering unsupported by current API.
- Results use direct headings, update time and receipt troubleshooting guidance.
- Campaign detail suppresses draft actions after acceptance, reconciles backend
  state, and blocks uncertain resubmission. Review found polling continued after
  terminal status; latest edit restricts polling to reconciling DRAFT or
  QUEUED/PROCESSING. No dedicated terminal-poll regression yet.
- BFF server.go extends an explicit allowlist for mounted app-scoped template/
  segment writes, memberships, user devices and PATCH /profile. No password,
  staff/grant, provider or dispatch-control routes added.
- New internal/dashboard/allowlist_test.go tests supported/denied paths, retained
  CSRF and upstream 403 authorization propagation.
- Root cause fix: main.tsx formerly passed makeQueryClient() directly in a Solid
  reactive provider property, producing distinct clients. It now constructs one
  client per root. Cache invalidation now propagates profile/membership/status.
- client.ts supplies JSON Content-Type on empty-body mutations required by BFF;
  the actual body stays empty for membership/delete contracts.
- Templates update their cached detail after save to avoid stale copied content.
- Docs: docs/specs/dashboard-operations.md, updated DASHBOARD-CAPABILITIES.md and
  web/dashboard/DESIGN.md. New FE1/FE2 findings appended to code-review.md.

## Verification and next blocker

- Full Go race run: **1510 passed, 0 skipped, 0 failed**, 48 packages.
  Command: python3 scripts/quiet-go-test.py -- -race ./...
  Full log: tmp/codex-logs/go-test-7378yj0z/full.log.
- Focused Go race including new allowlist/CSRF tests passed.
- Frontend build/typecheck passed. Vitest 7 passed. ESLint passed before latest
  small detail/test edits; rerun final lint/format after fixing pending test.
- Earlier E2E run: 12/12 passed (real BFF against isolated TLS fixture).
- **Latest E2E: 14 passed, 1 failed**. Do not claim all tests pass.
  Failing test: `a delayed template cannot overwrite an editable message` at
  web/dashboard/e2e/dashboard.spec.ts around line 243. It gates the template
  response and expects title/body inputs disabled before releasing it. Inspect
  error-context/trace to determine whether Suspense renders Loading instead of
  inputs; do not weaken the protection or silently remove the regression.
  Title/body controls currently disable while selected template is unresolved.
  Error context: web/dashboard/test-results/dashboard-a-delayed-templa-eca9b-erwrite-an-editable-message-chromium/error-context.md.
- Latest logs: /tmp/hello-dashboard-ux-{build,e2e,lint,unit,go,format}.log.
- Screenshots: web/dashboard/test-results/dashboard-settings-dark.png and
  dashboard-segment.png (visually checked dark settings), dashboard-mobile-logs.png.
- Bundle script reports initial JS 13.5 KiB gzip, but this appears to count only
  entry, excluding statically imported shared chunk (~50 KiB gzip). Do not claim
  13.5 KiB as full initial payload. Consider correcting measurement before final.
- Node default is incompatible 20.10.0. Use
  PATH=/Users/koperasiastra/.nvm/versions/node/v22.23.4/bin:$PATH before pnpm.
- Socket/browser/SSH tests need exec escalation. One previous fixture process
  (93773) was explicitly identified and stopped; test runner normally cleans up.

Next: fix delayed-template regression, review remaining error/disposal paths,
verify terminal polling and final mobile/dark views, rerun impacted checks plus
build/lint/format/budget, required Go build/vet/module/vulnerability checks as
appropriate. Split coherent commits without including unrelated changes, push
through existing CI/CD, follow deployment to staging and verify read-only UI.
No new real push is authorized. Production remains out of scope.

## Independent review

Agent /root/frontend_contract_search reviewed contracts and changes. Required
findings: uncertain open-dialog resubmission (fixed handler/button), polling
forever after terminal (fixed expression), possible delayed template overwrite
(controls already disabled; regression currently failing, investigate). No
further review response is required to resume independently.

## Supported backend contracts

Mounted handlers are internal/features/applications/{module.go,templates.go,
segment_reads.go,segment_writes.go,user_reads.go,campaign_access.go}.
Templates: GET list/detail, POST, PUT; no delete. Segments: GET list/detail/
members, POST, PUT, DELETE; membership PUT/DELETE by user ID. Users: GET list/
detail/devices. Lists accept only after_id/limit. Profile GET/PATCH is mounted.
Use generated staff types. Legacy features/{segments,users,notifications} global
routes are unmounted. No dynamic-segment, global search/status filter or generic
settings endpoint. Do not invent these capabilities.

## Preserve unrelated dirty work

Already dirty before this task: code-review.md (earlier edits),
docs/analysis/production-readiness-plan.md, docs/runbooks/production-images.md,
many untracked deploy/staging provisioning scripts/README and __pycache__,
scripts/__pycache__. Do not stage all. code-review.md contains our appended
FE1/FE2 section plus unrelated prior hunks; stage only our contribution.
New docs/analysis/dashboard-ux-review-2026-11-13.md is our prior audit/receipt note.

## Staging and deferred receipt work

VM: ssh kafka-vps => haidar@10.1.16.109:10022, office/VPN required.
Dashboard https://10.1.16.109:8443/login; current deployed/main remote last known
SHA 02f813ad4562e8a84e204daf8e3708ccdb70cbcc. Five roles healthy previously.
Trusted ops root /home/haidar/hello-staging-ops; read runbook staging-dashboard.md
and existing staging.py/CD workflow. Do not export private credentials/backups.
Keep TLS verification, dispatch policies and private network isolation intact.

Receipt investigation is explicitly deferred. Exactly one real FE send was made:
campaign 13, app 63677e77-2661-43b8-8253-35c1134bcc60, user 1. Provider accepted;
owner confirmed appearance and tap. Backend had no DELIVERED/OPENED observations;
Android showed waiting for a notification even after retry. Source of failure is
unproven. Protected approval journal on VM fe-pilot-20261113-campaign-13.json
records user confirmation. Pilot remains PILOT revision 6, same one-device cohort;
do not alter sending blocks or send again. Android source is
/Users/koperasiastra/projects/hello-fcm-smoke. Current task is frontend, not receipts.
