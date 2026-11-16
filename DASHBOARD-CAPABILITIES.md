# Staff dashboard: MVP capability map

Status: the staff journey, administration, operational workflows, and optional
application setup entrypoint are implemented locally. Local check results are dated
evidence, not a current full clean run: see the
[administration checks](docs/specs/dashboard-administration.md#verification-status)
and [Astryx migration validation](web/dashboard/README.md#astryx-migration-validation-2026-11-15).
Target staging acceptance, interactive UAT, and production release remain separate
validation gates; this map does not assert they have passed.

## Objective and assumptions

Build a web dashboard in `web/dashboard/` so staff can use the existing
notification backend without manually composing Postman requests. The first
release serves staff with existing accounts and explicit application grants.
The primary journey is sign in, choose an application, prepare a campaign,
confirm submission and inspect its results.

This is the product scope map, not the technical implementation manual. The
canonical [dashboard technical guide](web/dashboard/README.md) covers the current
React 19/Vite/TypeScript, React Router, React Query, Astryx, and same-origin Go BFF
implementation. The original SolidJS/Tailwind/Kobalte/Solid Query prescription is
superseded by that migration, not an alternative active stack. Backend
authorization rules remain authoritative.

Maintained interface copy, source comments, and technical documentation are
English; the original Indonesian-copy direction is superseded. The selected
[design contract](web/dashboard/DESIGN.md) uses Astryx only, with Stone as default
and Y2K/Matcha available. It supersedes the original green/neutral visual direction.
Mobile layouts must preserve essential workflows without horizontal page overflow.

## Capability map

| Module ID                | Responsibility                                                                                                                        | Depends on                                                                                   |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| staff-session            | BFF login/logout, private credential storage, browser session and safe refresh                                                        | Existing staff auth API                                                                      |
| application-workspace    | Signed-in layout, profile summary, granted application picker and scoped navigation                                                   | staff-session                                                                                |
| campaign-drafts          | Campaign list/detail, draft creation/replacement/deletion, scoped recipient lookup                                                    | application-workspace                                                                        |
| campaign-acceptance      | Explicit immediate-send or schedule confirmation, stable idempotency and conflict handling                                            | campaign-drafts                                                                              |
| delivery-results         | Per-campaign statistics and paginated delivery/read history                                                                           | application-workspace                                                                        |
| dashboard-administration | Staff account management, managed application registry, explicit grants and scoped operational reads                                  | staff-session, application-workspace                                                         |
| application-setup        | Optional registry-linked profile configuration, FCM candidate staging, OAuth verification and dispatch hold; no activation or release | dashboard-administration, protected onboarding policy and verifier scope                     |
| frontend-release         | Repeatable builds, CI, private staging deployment, health checks and rollback                                                         | staff-session, application-workspace, campaign-drafts, campaign-acceptance, delivery-results |

Build order: staff-session -> application-workspace -> campaign-drafts ->
campaign-acceptance -> delivery-results -> frontend-release.
Local verification accompanies every module; the final release module integrates
the complete tested journey with staging. Production enablement has separate
environment and operational gates.

## Screen flow

1. **Sign in:** email and password, generic invalid-credential feedback, pending
   submission protection and expired-session recovery. Never ask staff to enter
   the shared API key.
2. **Choose an application:** show only granted applications, their actual
   lifecycle status and an explicit empty state when no grant exists. An inactive
   application must not lead to an apparently usable campaign workspace.
3. **Campaigns:** browse the backend-supported cursor pages. Show the actual list
   fields (ID, title and status). Do not invent list dates, global totals,
   search/filter support or aggregate charts absent from the API.
4. **Campaign detail/editor:** title, body and recipient selections, with scoped
   user/segment lookups and broadcast handled as an exclusive selection. Use
   optional existing templates only after their read contract is included in the
   module spec. Only DRAFT campaigns expose edit/delete controls. Confirm
   deletion and preserve user input on a correctable error. No automatic saves
   that overwrite another request's results.
5. **Submission confirmation:** identify the application, campaign content,
   selected audience and immediate/scheduled action. Show the schedule timezone
   and convert to the backend's RFC3339 timestamp. Never present selected targets
   as a verified final recipient count. Do not retry an uncertain write with a
   fresh idempotency key or assume acceptance after a timeout.
6. **Results:** distinguish durable acceptance, provider acknowledgement, device
   observations and opens. Handle incomplete snapshots as unavailable data,
   rather than zero. Provide delivery/read pagination without implying the
   counters are mutually exclusive or have identical units.

Every screen needs loading, empty, retryable-error and keyboard-focus states.
Preserve context when navigating campaign detail back to its list. A revoked
grant, forbidden operation or expired session must not leave stale privileged
data visible as if access remains valid.

## Security and business boundaries

- The API requires `X-API-Key` even for login/refresh/logout. Keep that key and
  access/refresh credentials in the trusted BFF. No credentials in browser
  bundles, public environment variables, URLs, localStorage or logs.
- Use an opaque browser session with Secure, HttpOnly and appropriate SameSite
  cookies, CSRF protection and bounded lifetime. Session storage, encryption,
  restart behavior and refresh synchronization require an explicit module spec.
- Serialize refresh per family and replace both credentials atomically. An
  uncertain refresh must require login again; replay can revoke the family.
- Keep UUID application IDs and decimal business IDs as strings. Every BFF
  operation must validate inputs and preserve backend application scope.
- The backend remains authoritative for current permissions and grants. Role
  hints are insufficient authorization; no effective-permissions endpoint is
  currently available. Handle 401/403/404/409 and dispatch-hold 503 explicitly.
- HTTP 202 means durable acceptance, not provider or device delivery. The UI
  cannot open/close the staging pilot or bypass lifecycle/dispatch policies.
- Building a send interface does not authorize sending real notifications.
  Automated checks use isolated fixtures; real-device tests require explicit
  authorization for the recipient and send count.

## Original first-release boundary and current scope

- Global cross-application analytics or invented dashboard metrics.
- Application setup now supports configured enrollment profiles/platform mappings,
  FCM candidate upload and OAuth verification from registry detail, plus readiness,
  operation/approval metadata and dispatch hold. This optional protected capability
  is unmounted by default; it requires server policy, a credential provisioner,
  approved profiles and per-application verifier scope. See the
  [onboarding API](docs/api/application-onboarding-api.md) and
  [local walkthrough](docs/local-development.md#try-onboarding-in-the-dashboard).
  Saving setup or passing OAuth does not promote credentials, activate an
  application, enroll a device, prove target-project IAM/delivery, or release
  dispatch. Activation, device enrollment/testing, cohort creation and full
  delivery release are not provided by this setup screen.
- Campaign cancellation remains unsupported because the backend has no
  cancellation API.
- Bulk imports, user mutation and a rich visual message builder. The subsequent
  [operations expansion](docs/specs/dashboard-operations.md) adds scoped template
  and manual segment management, audience/device inspection and profile settings.
- Staff CRUD/status, administrative password reset, managed application
  registry, staff grants, read-only upload/audit history, and self-service
  password changes were outside the original first-release boundary. They are
  now implemented locally as described in the [dashboard administration
  expansion](docs/specs/dashboard-administration.md), which records dated local
  verification rather than target acceptance.
- Self-registration, forgotten-password recovery and third-party sign-in. Staff
  administration can reset another account's password, and signed-in users can
  change their own password as described in the administration expansion.
- iOS/client SDK development, production provider configuration or unrestricted
  notification sending.

## Acceptance examples to expand into module specifications

- A valid staff account can sign in, select only a granted ACTIVE application,
  create a draft and reopen its saved content.
- Unauthorized actions fail safely even if a user crafts a browser request or
  loses a grant while the page is open.
- An expired access credential causes at most one concurrent refresh per family;
  uncertain rotation returns to sign-in without a replay loop.
- A draft can be edited/deleted; a non-draft cannot. Unknown write outcomes do
  not silently create duplicates.
- An explicitly confirmed submission shows acceptance separately from device
  observation. Holds/conflicts retain understandable actionable feedback.
- Empty history, incomplete statistics and upstream failure each have distinct
  states. No fabricated analytics appear in staging.
- The principal journey works with keyboard navigation and narrow screens.
- The tested build can be deployed to staging and rolled back together with
  compatible BFF configuration; production credentials are separate.

## Source contracts

- [Staff authentication](docs/api/auth-api.md)
- [Application registry and grants](docs/api/applications-api.md)
- [Optional application onboarding setup](docs/api/application-onboarding-api.md)
- [Campaign drafts](docs/api/scoped-campaign-drafts-api.md)
- [Campaign acceptance and scheduling](docs/api/campaign-acceptance-api.md)
- [Delivery history and statistics](docs/api/scoped-campaign-history-api.md)
- Canonical wire contracts: `docs/contracts/staff-business.openapi.json` and
  `docs/contracts/application-registry.openapi.json`.

Local implementation covers the browser journey, in-memory BFF sessions, draft
workflows, explicit submission, history and a frontend CI gate. Subsequent
expansions add templates, audience/segment operations, administration and optional
application setup. See the dated evidence linked above; no fresh full-suite run
is asserted here. Advanced table/form/chart dependencies remain deferred, not
active product requirements.

Protected deployment configuration, authenticated gateway transport, container
and CD integration are implemented and covered by local checks. Staging
activation and acceptance follow the staging dashboard runbook. Interactive UAT
and production release gates remain separate; local fixtures do not prove target
readiness.
