# Dashboard foundation and BFF

## Current decisions and document ownership

The implemented foundation is a React 19/Vite/TypeScript dashboard with React
Router, React Query, Astryx, and a separate same-origin Go session BFF. The
original Solid/Vite foundation decision is superseded by the Astryx migration;
it is not an alternative supported frontend.

The [capability map](../../DASHBOARD-CAPABILITIES.md) owns product scope. The
[dashboard README](../../web/dashboard/README.md) is the canonical technical guide
for architecture, setup, commands, session/write semantics, and release gates.
The [design contract](../../web/dashboard/DESIGN.md) owns visual decisions:
Astryx-only guidance, Stone by default, with Y2K and Matcha available. The earlier
green/neutral design direction is superseded. This spec records foundation
boundaries rather than duplicating the operating manual.

## Security and consistency boundaries

- The BFF uses a fixed, verified TLS upstream and an explicit endpoint allowlist.
  Access/refresh credentials and the shared API key remain server-side.
- Bounded in-memory sessions support one replica, expire after twelve hours, and
  are lost on restart. Anonymous CSRF sessions use a separate capped pool with a
  five-minute expiry; exhausting it must not evict authenticated sessions.
- Session IDs rotate at login. Mutations, including login/logout, require exact
  Origin and session CSRF checks. HTTPS cookies are Secure, HttpOnly and
  SameSite=Strict; development HTTP is restricted to loopback.
- Refresh is serialized per session, attempted once after a definitive 401, and
  never replayed after uncertain I/O. Unknown mutation outcomes must not create
  duplicate writes; preserve each API's idempotency and version contracts.
- Backend database-backed authorization and grants remain authoritative. Query
  data stays application-scoped and in browser memory; disposed editors must not
  navigate after delayed responses. Unsaved changes require explicit handling.
- The optional registry-linked setup screen configures profiles, stages FCM
  candidates and requests OAuth verification. Its protected server policy and
  per-application verifier scope are not enabled by building the frontend. Setup
  success is not credential promotion, application activation, device enrollment
  or dispatch release. See the [setup API](../api/application-onboarding-api.md)
  and [local walkthrough](../local-development.md#try-onboarding-in-the-dashboard).

## Evidence and acceptance boundaries

The [administration verification record](dashboard-administration.md#verification-status)
and [2026-11-15 migration record](../../web/dashboard/README.md#astryx-migration-validation-2026-11-15)
are dated local evidence. In particular, the migration record combines a full
browser run with focused reruns; it is not one final all-green suite run and is
not evidence that later changes pass. Use the canonical technical guide for
current verification commands, including type/build, lint, format, unit,
fixture-browser, bundle and Astryx checks.

Protected deployment configuration, ingress, container hardening and CI/CD are
implemented separately from local development. Their existence does not prove
target staging acceptance, interactive UAT or production readiness. Those gates
require separate evidence. Automated verification must not send real
notifications; real-recipient tests require explicit approval.
