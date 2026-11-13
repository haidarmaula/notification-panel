# Dashboard UX review — 2026-11-13

Scope: source review of the current dashboard and evidence from the real staging
campaign 13 workflow. This is a product backlog, not a completed browser or
accessibility audit. Proposed modules require explicit API/BFF contracts before
implementation; existing backend capability must be verified separately.

## Deferred Android receipt investigation

Campaign 13 was sent once through the staging frontend. The provider accepted
the message, and the owner confirmed that it appeared on Android and was tapped.
At the last verification, the delivery intent was SENT with provider acceptance,
but observed_delivered_at and observed_opened_at were absent. The Android app
displayed `Receipts: waiting for a notification.` before and after Retry receipts.
This does not establish whether the fault is in the mobile callback, payload
validation, session context, or backend ingestion. Do not attribute it solely to
Android, fabricate receipt events, or resend without a new explicit approval.
Resume by tracing payload metadata, callback execution, enrolled context and
receipt HTTP responses without logging credentials or receipt capabilities.

## Priority backlog

| Priority | Gap and user impact | Proposed acceptance criteria |
| --- | --- | --- |
| P0 | Campaign detail appeared to retain Draft/actions after the successful send during UAT. This may mislead the operator. | Reproduce and fix state freshness; accepted work promptly loses draft-only actions; uncertain writes remain explicitly unresolved until reconciled. |
| P0 | Environment and send restrictions are not visible in the workspace shell. Operators can confuse local/staging or discover a hold only after submission. | Show an authoritative environment label and, when an API contract exists, read-only pilot/hold status with a recovery explanation. Never add a browser bypass of dispatch controls. |
| P1 | Desktop navigation cannot collapse; mobile already has an open/close drawer. | Add a persistent desktop compact mode, named icon controls and tooltips; separately verify mobile Escape, focus return and keyboard containment. |
| P1 | Campaign list has title, ID and status, but no search/filter controls or operational metadata. | Introduce supported search and status filters, preserve list context in the URL, and add actual dates/audience/results metadata only when exposed by the API. Never filter one loaded page as if it were the entire dataset. |
| P1 | Repeated message creation lacks a template workflow. | Add template list/editor and create-from-template flow with preview and validation; verify backend CRUD, permissions and variable support first. |
| P1 | Recipient lookup is embedded in the editor; no dedicated audience/device investigation surface exists. | Add scoped audience/segment/device views with eligibility explanations and permission checks. Separate user counts from installation counts. |
| P1 | Results explain different measurement units, but missing receipts offer little diagnostic guidance. | Show freshness, measurement definitions, unknown versus zero, and actionable missing-receipt guidance. Preserve distinct provider, device and open evidence. |
| P1 | Account and application settings have no dedicated navigation. | Separate personal preferences from application settings and privileged team/access administration; sensitive mutations require supported backend contracts and current authorization. |
| P2 | No dark/system appearance preference. | Implement semantic theme tokens, Light/Dark/System selection and persistence; verify contrast and all loading/error/disabled/focus states in each theme. |
| P2 | No overview for routine operations. | Add an application landing view with recent campaigns, upcoming sends and actionable issues backed by real queries. Avoid decorative or unsupported totals. |
| P2 | Copy is sometimes promotional rather than operational. | Use direct headings such as Create campaign and Delivery results; keep language and terminology consistent. |
| P2 | Repeated operations require navigating through individual detail pages. | Consider duplicate-as-draft, preview improvements and clear schedule timezone after validating contracts. Do not offer unsupported cancellation or bulk sending. |

## Proposed navigation

Within the selected application: Overview, Campaigns, Templates, Audience,
Delivery logs, and Application settings. Keep API reference under help/developer
resources. Put personal profile/preferences in the account menu and privileged
team/access controls in a clearly separate administration area. A separate
Analytics section is warranted only when actual reporting needs exceed the
overview and campaign results. Do not ship empty menu destinations.

## Recommended delivery order

1. Reconcile campaign state after mutations; improve environment/status clarity.
2. Improve the shell: collapsible navigation, theme preference, clear breadcrumbs
   and account menu, with keyboard and small-screen verification.
3. Improve campaign discovery and list context with API support where necessary.
4. Deliver templates, audience inspection and scoped settings as complete flows.
5. Add operational overview and richer reporting using real aggregate contracts.

References consulted: [Anti-slop](https://github.com/miqdadbadjuber/anti-slop)
and [Impeccable](https://impeccable.style/docs/). Existing campaign functionality,
explicit confirmation, preview, draft protection, and separation of receipt
metrics should be preserved.
