# Staging staff dashboard

The Solid application and Go BFF share the existing private HTTPS gateway at
`https://10.1.16.109:8443/login`. Office/VPN firewall rules remain authoritative.
The browser must trust the staging CA. Use existing staging staff credentials;
never use the isolated local fixture account against staging.

## Trust and process boundaries

The dashboard image contains static production assets and a Go binary; Node and
pnpm are build tools only. The runtime is nonroot, readonly, drops all capabilities
and mounts one root-owned policy volume readonly at `/etc/hello`. Deployment
accepts only `--deployment` and `/etc/hello/dashboard.json`, validated using the
shared ownership, ancestor-directory, mode and ACL checks. Environment secrets or
configuration path overrides cannot affect this mode.

Gateway-to-BFF traffic uses a dedicated server CA and gateway client CA on
loopback 18443. BFF-to-API traffic uses verified TLS plus a dedicated client
identity authorized by the existing API gateway-client CA. Forwarded client IPs
come only from authenticated, pinned gateway peers. The BFF pins upstream TLS
trust and hostname, disables proxies/redirects and allows only explicit API paths.
`/api/v1/` continues to reach the API directly; `/docs/` retains its static route.
All other application paths and `/bff/` reach the BFF.

Verified gateway clients have per-IP/general/concurrency limits, with separate
login/logout budgets. Anonymous sessions have bounded capacity and five-minute
expiry. Authenticated sessions stay in memory for at most twelve hours. A
replacement/restart requires login again. Refresh is serialized and an uncertain
response invalidates the session without replay. Secure/HttpOnly/SameSite=Strict
cookies and exact-origin/session-CSRF checks protect browser writes.

Private management mTLS serves `/livez`, `/readyz` and minimal process metrics on
19105. Readiness verifies the API TLS/authentication boundary; existing API health
checks independently verify database readiness. Scheduled health also tests the
public dashboard HTML and anonymous secure-cookie boundary.

## Initial activation and CD

1. Install reviewed operations helpers with
   `python3 deploy/staging/staging.py install`. Until bootstrap commits its marker,
   commands retain the existing four-role inventory.
2. Optionally build a committed candidate without activation using
   `python3 deploy/staging/staging.py prepare --ref HEAD --schema 41 --release <id>`.
   This is a preflight, not a replacement for hosted CI authorization.
3. Push reviewed main. All hosted gates, including dashboard workflows and seven
   offline production images, must pass before the signed staging marker exists.
   CD deploys the existing backend roles and records the checked dashboard image.
4. Run `python3 deploy/staging/staging.py dashboard-bootstrap`. It requires a
   completed signed CD release, uses that immutable image, provisions transport
   identities and policy, checks readiness, then journals the gateway change.
   It does not alter dispatch/pilot policy or send notifications.
5. A successful bootstrap records an explicit five-role baseline. The initial
   baseline has no executable rollback; `recover` refuses to treat it as an
   incomplete deployment. The next CD poll notices the inventory change and
   deploys the same signed source once under a distinct dashboard release ID.
   Later pushes replace all five roles with ordinary coordinated rollback.

Bootstrap and CD share the CD lock, and inventory refresh happens after command
lock acquisition. This prevents a process imported before bootstrap from later
activating a four-role manifest. Health/backup commands likewise refresh their
inventory after acquiring their lock. Old four-role status records remain valid
until the explicit dashboard marker exists.

Incomplete bootstrap is recovered by rerunning `dashboard-bootstrap`: restore
original gateway routing and deployment journal, stop/remove only the new BFF,
remove its new policy volume/identities and clear its marker. Normal release
recovery remains `python3 deploy/staging/staging.py recover`; rollback remains
`python3 deploy/staging/staging.py rollback`. Neither operation clears unrelated
holds. If gateway recovery fails, inspect protected failure logs before retrying.

Encrypted VM backups automatically include the dashboard policy volume through
the active role inventory and its gateway identity through `/etc/hello` backup.
No off-host backup export is required by this rollout. Keep certificate expiry
and eventual rotation in the operator review; initial transport certificates are
issued for one year. The management server identity follows the existing shared
management plane.

## Acceptance and limitations

Check login page/assets, anonymous secure cookies, real staff login/logout,
granted applications and read-only campaign access. Verify unauthenticated
private TLS clients cannot reach the BFF, and `/api/v1/` and `/docs/` still work.
Record role readiness, clean schema and unchanged dispatch policy. Do not send a
notification as a smoke test. Interactive UAT, accessibility on target devices,
production capacity and production configuration remain separate release gates.
