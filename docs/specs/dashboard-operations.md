# Dashboard operations expansion

Implemented locally on 2026-11-13. Staging verification is recorded separately;
this document does not claim production readiness.

## Scope and contracts

- Application overview: five recent campaigns, actual statuses, links to results
  and operational workflows. No fabricated totals or scheduled-send aggregates.
- Templates: scoped list, create, replace and deactivate through the mounted
  application API. Active templates prefill a new draft and retain template
  attribution. Template edits propagate to the next draft immediately. There is
  no template delete API or assumed variable interpolation.
- Audience: scoped users and user device registration inspection. Active
  registration does not imply an online or reachable device.
- Manual segments: scoped list/create/replace/delete plus paginated membership
  add/remove. There are no dynamic membership rules or inferred recipient totals.
- Delivery logs: campaign selection leading to scoped delivery/read history.
  This is not an aggregate cross-campaign log search.
- Settings: Light/Dark/System, browser-local sidebar preference, current staff
  profile name/email updates, and read-only application identity. Credentials,
  team permissions, provider configuration and dispatch controls remain outside
  this interface.

The BFF extends only its explicit allowlist. Current backend DB authorization,
application grants and CSRF remain authoritative. No API keys or access/refresh
credentials enter browser storage. Only appearance/sidebar preferences persist.
Unknown mutation outcomes block repeat submission until the operator reconciles
the state. Forms warn before discarding unsaved edits.

## Navigation and state

Application navigation exposes Overview, Campaigns, Templates, Delivery logs,
Audience users/devices, Segments and Settings. The desktop sidebar can be hidden;
the narrow-screen drawer contains keyboard focus and returns it on Escape.
The header displays LOCAL on loopback and the actual host/port elsewhere so
operators can distinguish environments without inventing a deployment label.

One QueryClient is created per frontend root. Writes invalidate shared observers;
successful acceptance removes draft actions immediately and reconciles actual
backend status. Active queued/processing work is polled, without resubmitting.
Campaign cursor navigation is encoded in the URL. Exact campaign ID lookup is
supported; global text search/status filtering awaits an API contract.

## Verification

Use the dashboard build, lint, format, unit, bundle-budget and Playwright commands
in `web/dashboard/package.json`. Browser tests use the real BFF against an
isolated TLS fixture, never staging or a push provider. BFF race tests cover the
new allowlist, denied paths, retained CSRF and upstream authorization denial.

The receipt investigation for real campaign 13 remains deferred in
`docs/analysis/dashboard-ux-review-2026-11-13.md`. No additional notification is
authorized by this frontend implementation.

## Remaining product work

Server-supported text search/status filters, dynamic segments, aggregate
analytics, a scheduled-send overview, team/role/grant administration and provider
settings need separate contracts and implementation. Keep these absent rather
than shipping nonfunctional menu destinations or misleading current-page filters.
