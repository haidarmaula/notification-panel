# Staff dashboard: MVP capability map

Status: MVP journey and Solid/Vite stack approved. Staff administration,
managed application registry, grants, scoped audit/upload reads, and self-service
password change are implemented locally; local frontend/browser verification
is pending. Staging and production deployment remain separate validation
milestones. See the [dashboard administration expansion](docs/specs/dashboard-administration.md).

## Objective and assumptions

Build a web dashboard in `web/dashboard/` so staff can use the existing
notification backend without manually composing Postman requests. The first
release serves staff with existing accounts and explicit application grants.
The primary journey is sign in, choose an application, prepare a campaign,
confirm submission and inspect its results.

The selected stack is SolidJS, Vite and TypeScript, with a same-origin Go BFF.
Use Tailwind, Kobalte, Solid Signals/Stores and TanStack Solid Query. Add table,
form and chart dependencies when their concrete workflows require them.
Keep the backend Go layout and its authorization rules intact.

Product copy starts in Indonesian. Source comments and maintained technical
documentation stay in English. Reuse Hello's existing restrained green/neutral
visual identity, with compact navigation, readable forms and data tables.
Mobile layouts must preserve the essential workflows without horizontal page
overflow. Detailed wireframes and component choices belong to the design phase.

## Capability map

| Module ID | Responsibility | Depends on |
| --- | --- | --- |
| staff-session | BFF login/logout, private credential storage, browser session and safe refresh | Existing staff auth API |
| application-workspace | Signed-in layout, profile summary, granted application picker and scoped navigation | staff-session |
| campaign-drafts | Campaign list/detail, draft creation/replacement/deletion, scoped recipient lookup | application-workspace |
| campaign-acceptance | Explicit immediate-send or schedule confirmation, stable idempotency and conflict handling | campaign-drafts |
| delivery-results | Per-campaign statistics and paginated delivery/read history | application-workspace |
| dashboard-administration | Staff account management, managed application registry, explicit grants and scoped operational reads | staff-session, application-workspace |
| frontend-release | Repeatable builds, CI, private staging deployment, health checks and rollback | staff-session, application-workspace, campaign-drafts, campaign-acceptance, delivery-results |

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
- Application lifecycle activation and pilot policy controls remain unsupported.
- Campaign cancellation remains unsupported because the backend has no
  cancellation API.
- Bulk imports, user mutation and a rich visual message builder. The subsequent
  [operations expansion](docs/specs/dashboard-operations.md) adds scoped template
  and manual segment management, audience/device inspection and profile settings.
- Staff CRUD/status, administrative password reset, managed application
  registry, staff grants, read-only upload/audit history, and self-service
  password changes were outside the original first-release boundary. They are
  now implemented locally as described in the [dashboard administration
  expansion](docs/specs/dashboard-administration.md); local frontend/browser
  verification is pending.
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
- [Campaign drafts](docs/api/scoped-campaign-drafts-api.md)
- [Campaign acceptance and scheduling](docs/api/campaign-acceptance-api.md)
- [Delivery history and statistics](docs/api/scoped-campaign-history-api.md)
- Canonical wire contracts: `docs/contracts/staff-business.openapi.json` and
  `docs/contracts/application-registry.openapi.json`.

Local implementation covers the browser journey, in-memory BFF sessions, draft
workflows, explicit submission, history and a frontend CI gate. The administration
expansion adds account, registry, grant, upload/audit-read and self-password
workflows; its local frontend/browser verification is pending. Advanced
table/form/chart dependencies and template selection remain deferred.

Protected deployment configuration, authenticated gateway transport, container
and CD integration are implemented and covered by local checks. Staging
activation and acceptance follow the staging dashboard runbook. Interactive UAT
and production release gates remain separate; local fixtures do not prove target
readiness.
