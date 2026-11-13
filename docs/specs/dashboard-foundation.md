# Dashboard foundation and BFF

Implementation scope: Solid/Vite dashboard, Go BFF, login/session, granted
application workspace and scoped campaign workflows. The approved module map is
in `DASHBOARD-CAPABILITIES.md`. No production readiness or deployment is asserted.

The BFF uses fixed, verified upstream TLS and an explicit endpoint allowlist.
Opaque access/refresh credentials remain server-side. A bounded in-memory session
store supports one replica; sessions expire after twelve hours and restart logs
users out. Session IDs rotate at login. Browser mutations require exact Origin
and a session CSRF token, including login/logout. Cookies are Secure, HttpOnly,
SameSite=Strict in HTTPS environments; local HTTP is restricted to loopback.
Refresh is serialized per session, attempted once after a definitive 401 and
never retried after uncertain I/O. Forwarded mutations require stable idempotency
where supported. Go backend authorization and grant checks stay authoritative.

The first delivery is locally reviewable. Protected staging configuration, ingress,
container hardening, CI/CD integration and production gates follow local checks.
No real notification may be sent by automated verification.

Anonymous CSRF sessions use a separate capped pool with a five-minute expiry.
Exhausting that pool evicts an anonymous entry, never an authenticated session.
Route-local state remounts on pathname changes; a disposed editor cannot navigate
after a delayed save response. Query data remains scoped and in browser memory.

Validation: focused Go race tests for CSRF/session isolation, concurrent refresh,
logout and uncertainty; Vitest for HTTP/state validation; Playwright with isolated
API fixtures for keyboard/login/application/campaign flows, errors and responsive
layout. Build and type-check production assets; measure emitted JS gzip size.

Commands:

```sh
go test -race ./internal/dashboard/...
go build -o /tmp/hello-dashboard ./cmd/dashboard
cd web/dashboard
pnpm install --frozen-lockfile
pnpm build
pnpm test
pnpm test:e2e
```
