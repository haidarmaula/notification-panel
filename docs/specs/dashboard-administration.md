# Dashboard administration expansion

Administration was implemented and locally checked on 2026-11-14. This document
records scope, API boundaries and dated evidence, not a current full clean run or
staging/production acceptance. The [dashboard technical guide](../../web/dashboard/README.md)
is canonical for the current React/Astryx architecture and verification commands.
The later optional setup entrypoint is described below; the original checks do
not establish its verification status.

## Staff accounts

The dashboard provides a staff directory with name/email search and pagination,
staff detail, account creation, profile/role updates, active-status changes, and
account deletion. Roles are selected by their seeded names: `SUPER_ADMIN`,
`ADMIN`, `OPERATOR`, and `VIEWER`. The UI confirms destructive account removal.

Administrative password reset is a separate operation on a staff account. The
acting administrator supplies their own current password and a replacement
password for the target account. This does not ask for the target account's old
password. The current API requires 8–72 bytes for the replacement; backend reset
rules and current authority remain authoritative.

The administration menu is presented when the current `/profile` response says
the account is active and has the `SUPER_ADMIN` role. This is a navigation hint
only. The backend's current database-backed authorization guards remain
authoritative for every staff operation; a JWT role claim or hidden menu is not
an access control.

## Managed application registry and grants

The registry supports paginated listing and exact-key filtering, application
creation, detail, and name changes. New applications are created in
`PROVISIONING`. The registry exposes the backend's strong ETag on versioned
records; rename uses the version read by the browser. A stale `412` requires the
operator to reload and review the current record before confirming another
change. Writes are not retried automatically with a fresh version.

Staff grants can be listed, inspected by staff ID, created, revoked, and
reactivated. A missing grant detail (`404`) is the create path and uses
`If-None-Match: *`; a denied request remains an error and is not treated as an
absent grant. Existing grant updates use `If-Match`, including reactivation,
and revocation also uses the inspected strong ETag. A stale `412` requires a
reload and explicit review before a new confirmation. Grant staff IDs remain
decimal strings in browser code.

Current backend checks for `applications.manage`, target staff state, and
resource scope remain authoritative. Creating an application does not grant
business access. The registry provides no application activation/status-change
route. Campaign cancellation is also outside this implementation because the
backend has no cancellation operation.

### Optional application setup extension

Registry detail now links to **Notification setup** at
`/apps/admin/applications/{app_id}/onboarding`. Authorized administrators can
configure an allowlisted enrollment profile and platform identifiers, stage an
FCM credential candidate, request/poll OAuth verification, inspect readiness and
safe operation/approval metadata, and hold dispatch. These are setup operations,
not device enrollment or activation.

The routes are optional and unmounted by default. Protected onboarding policy,
credential provisioner connectivity, approved profiles and database grants are
required; verification additionally requires per-application verifier scope.
Backend `applications.manage` checks remain authoritative. Setup writes preserve
ETags/idempotency, with explicit stale-version review and same-request retry for
unknown outcomes. Credential material is not persisted in browser storage or
query caches.

The screen can request reviewed activation, queue an approved test-cohort
dry-run, and request PILOT or FULL dispatch. Separate operator roles review
and apply lifecycle changes. Devices enroll through the mobile API. OAuth
verification confirms token issuance. See the
[onboarding API](../api/application-onboarding-api.md),
[technical guide](../../web/dashboard/README.md#optional-application-setup) and
[synthetic local walkthrough](../local-development.md#try-onboarding-in-the-dashboard).

## Scoped operational reads and account security

Within an application workspace, the dashboard offers read-only upload batch
history, batch row-validation status, and audit-log history. Upload history is
not an import workflow: the dashboard cannot upload files or create batches.
The views show only fields returned by their APIs and do not invent row-level
error details or audit payloads. Existing project grants and backend read
permissions govern these routes.

Staff can change their own password from account settings. The backend accepts
nonempty values with a maximum of 72 bytes for the new password; the dashboard
checks the byte limit and requires matching confirmation. The backend advances
the account session generation; after confirmed success, the BFF clears its
session cookie and the browser clears in-memory query state before returning to
login. The exact current-password domain failure is mapped by the BFF to `422`
so it does not trigger session refresh or logout. Other authentication `401`
responses retain normal refresh and expiry handling.

The browser preserves unsafe JSON int64 identifiers as decimal strings instead
of rounding them through JavaScript `Number`. IDs are validated before they are
placed into staff or grant paths.

## Verification status

The following is the 2026-11-14 administration record, before the React/Astryx
migration and later setup extension. Bundle sizes, captures and styling details
are historical, not current implementation requirements. See the
[2026-11-15 migration record](../../web/dashboard/README.md#astryx-migration-validation-2026-11-15)
for later evidence and its full-run/focused-rerun limits. Neither record is a
fresh full-suite run for the current tree.

Recorded with Node 22.23.4 and the repository Go toolchain:

- Frontend production build, ESLint, and full dashboard formatting checks passed.
- Frontend unit tests: 11 passed, including exact int64 identifiers, conditional
  write headers, stale-version rejection, and missing/weak ETag rejection.
- Full Playwright suite: 45 passed against the real BFF and isolated TLS fixture.
  New cases cover staff CRUD/status/reset, registry/grants and stale versions,
  denied reads, unsafe-size IDs, scoped upload/audit reads, password session
  handling, pending profile authority, uncertain writes, and responsive layout.
- Initial JavaScript: 63.7 KiB gzip against the 160 KiB budget.
- All six `scripts/ci.sh` checks passed: format, modules, build, test, vet, vuln.
  Tests include the repository race checks. The vulnerability scan reported no
  reachable vulnerabilities; it also noted one issue in imported packages and
  three in required modules that the code does not appear to call. Dependencies
  were not changed.
- Light/dark staff and registry captures were inspected at desktop and 320px.
  Wide staff tables scroll locally. A regression where absolute screen-reader
  header text escaped its static scroll container was fixed by containing its
  positioning inside `.table-wrap`; the document-width assertion stays strict.
- The new fixture persists profile updates so existing profile tests remain
  meaningful. No existing assertion was removed or relaxed to bypass a failure.

Temporary evidence: `/tmp/hello-admin-full-e2e.log`,
`/tmp/hello-admin-ci-{format,modules,build,test,vet,vuln}.log`, and captures in
`/tmp/hello-admin-ui/`. Local socket/network permissions were required for the
isolated tests and vulnerability database. Fixture servers were stopped after
verification. These temporary paths are historical references, not guaranteed
retained artifacts. The checks do not establish staging integration, production
readiness, full WCAG conformance, or real-device delivery. Target staging/UAT and
production gates require separate acceptance evidence.

## Superseded UI delivery gate

The original 2026-11-14 review recorded passing desktop/narrow-layout, API-field,
synthetic-data, interaction, build and bundle checks under its legacy R-/C-
checklist. Its two-theme styling and external design-detector expectations are
superseded, not current delivery requirements. Current UI work follows Astryx
only and the [design contract](../../web/dashboard/DESIGN.md): Stone by default,
with Y2K and Matcha available. Use the canonical technical guide's checks and
report blocked checks honestly; the historical PASS record is not current UI or
accessibility acceptance.

## Related contracts

- [Staff administration and self-service password contracts](../api/staff-api.md)
- [Profile and self-service password](../api/profile-api.md)
- [Application registry and grants](../api/applications-api.md)
- [Upload and audit reads](../api/scoped-upload-audit-api.md)
- [Dashboard operations expansion](dashboard-operations.md)
