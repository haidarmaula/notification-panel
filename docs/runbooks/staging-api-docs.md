# Staging API reference

Open `https://10.1.16.109:8443/docs/` while connected to the office network or
VPN. The browser must trust the existing staging CA. The documentation uses
the same private gateway and firewall policy as the API.

Choose one of three guides: Staff & campaigns, Applications & access, or
Mobile & receipts. The 57 operations come from the canonical OpenAPI contracts
in `docs/contracts/`. Download OpenAPI exports the selected contract for tools
such as Postman. Operation counts change with the contracts.

Use **Authorize** for the security schemes shown by each operation. Staff
requests use `X-API-Key`; protected operations also require the staff bearer
token. Mobile operations use the original account's mobile bearer token.
Credentials remain in page memory and are cleared on reload or guide changes.
The page makes no API calls until the user executes an operation. **Try it out
sends real staging requests**, including writes; the existing pilot restrictions
still apply. Never use production credentials here.

## Updates and rollback

Edit the canonical contracts and, when needed, `web/api-docs/`. Successful main
CI authorizes the existing staging CD receiver. It builds a deterministic
documentation artifact alongside the application images, checks its hashes,
publishes immutable release URLs, and switches the documentation pointer only
after backend readiness. It verifies the served files before reopening dispatch.
The deployment journal restores both backend containers and the previous docs
pointer on rollback. Captured releases preserve their current docs pointer.

The first deployment requires the trusted operations receiver to be installed,
then `python3 deploy/staging/staging.py docs-gateway`. This one-time command
extends the existing gateway, validates its nginx configuration and authenticated
API boundary, and retains a recovery journal and original gateway container.
Documentation lives in the separate `hello-staging-api-docs` volume, mounted
readonly in nginx. TLS credentials are never in the static document root.
Encrypted staging backups include this volume. Health checks verify the pointer
against the deployment journal and hash every served asset.

## Local checks and vendor updates

Run `python3 -m unittest discover -s deploy/staging -p '*_test.py'` and
`node --test web/api-docs/app.test.cjs`. Build an empty preview directory with
`python3 scripts/api_docs.py --output /tmp/hello-api-docs --release preview`.
The contract validator runs separately in CI.

Swagger UI assets are vendored from the official repository at the version and
commit recorded in `web/api-docs/vendor/manifest.json`; the upstream license is
included. Update the assets, provenance and hashes together, then verify all
three guides in a browser. No CDN or external validator is used. The request
interceptor permits only this origin's `/api/v1/` paths, with cookies omitted;
the gateway also applies a restrictive Content Security Policy.
