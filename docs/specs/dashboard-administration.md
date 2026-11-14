# Dashboard administration expansion

Implemented and verified locally on 2026-11-14. This document records the scope,
API boundaries, and local checks, not a staging or production acceptance result.

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

Verified with Node 22.23.4 and the repository Go toolchain:

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
verification. These checks do not establish staging integration, production
readiness, full WCAG conformance, or real-device delivery. Impeccable's automatic
detector remains unavailable as documented in the prior design audit.

## UI delivery gate

- PASS, R-03/R-34: both themes and narrow admin layouts inspected; strict page
  width checks pass at 320px, with table overflow confined to its container.
- PASS, R-17/R-36/R-38: UI fields follow mounted API contracts; test data is
  isolated synthetic fixture data, not production metrics or delivery claims.
- PASS, R-26/R-32/R-39: semantic links, named fields, native confirmation and
  existing focus styles retained; browser workflows exercise real controls.
- PASS, R-31/R-37: established design preserved; administration layout decisions
  are recorded in the dashboard design contract.
- PASS, R-35/R-40/C-5: production build, browser evidence and bundle measurement
  recorded with explicit integration and accessibility limits above.

## Related contracts

- [Staff administration and self-service password contracts](../api/staff-api.md)
- [Profile and self-service password](../api/profile-api.md)
- [Application registry and grants](../api/applications-api.md)
- [Upload and audit reads](../api/scoped-upload-audit-api.md)
- [Dashboard operations expansion](dashboard-operations.md)
