# Dashboard operations expansion

The original operations expansion was implemented locally on 2026-11-13. This
spec records product/API boundaries, not a current full clean run, target staging
acceptance or production readiness. The [dashboard README](../../web/dashboard/README.md)
is the canonical technical guide for the current React/Astryx implementation,
local commands and release gates; earlier frontend/design choices are superseded.

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
- Settings: Light/Dark/System, Stone/Y2K/Matcha theme choice, browser-local sidebar
  preference, current staff profile name/email updates, and read-only application
  identity. This workspace settings screen does not configure provider credentials
  or dispatch. Staff/grants and optional setup are separate administrative flows,
  not missing operations-screen features.

The later [administration expansion](dashboard-administration.md) implements
staff account, role-assignment and application-grant management. Its registry-linked
[optional setup screen](../../web/dashboard/README.md#optional-application-setup)
configures enrollment profiles/platform identifiers, stages FCM candidates,
requests and polls OAuth verification, reads readiness/approval metadata and can
hold dispatch. It also exposes reviewed activation, dry-run, and release
requests. The default server leaves setup unmounted; protected policy,
provisioner/profiles/grants and per-app verifier scope govern availability. See
the [onboarding API](../api/application-onboarding-api.md) and
[local walkthrough](../local-development.md#try-onboarding-in-the-dashboard).
Setup does not automatically promote credentials, activate an application,
enroll/test devices or release delivery. OAuth success is not target-project IAM
or device-delivery evidence.

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

Use the [technical guide's verification commands](../../web/dashboard/README.md#local-verification).
Browser tests use the real BFF against an isolated TLS fixture, never staging or
a push provider. BFF race tests cover the allowlist, denied paths, retained CSRF
and upstream authorization denial. The
[2026-11-15 migration record](../../web/dashboard/README.md#astryx-migration-validation-2026-11-15)
contains dated local results, including focused reruns, not one final clean full
run or proof that later setup changes pass. Target staging/UAT and production
gates require separate acceptance evidence.

The receipt investigation for real campaign 13 remains deferred in the
[2026-11-13 UX review](../analysis/dashboard-ux-review-2026-11-13.md). No additional
notification is authorized by this frontend implementation.

## Remaining product work

Server-supported text search/status filters, dynamic segments, aggregate
analytics and a scheduled-send overview still need separate contracts and
implementation. Staff account, role-assignment and application-grant administration
is now implemented separately; the original deferral is superseded. Provider setup is partially implemented via
the optional administration flow above, not a complete activation or release
workflow. Keep unsupported destinations absent rather than shipping nonfunctional
menus or misleading current-page filters.
