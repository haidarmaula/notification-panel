# M02e — Staff JWT parser baseline

Contract established 2026-11-03 following the M02 exit review. This is a scoped
parser safeguard, not acceptance of all ADR-11 identity/session decisions.
Status: implemented/local-verified, not deployed; [evidence](../analysis/m02e-verification.md).

Access and refresh parsing must accept only HS256, the appropriate signing
secret, the exact existing purpose (`access` / `refresh`), a required valid exp
and a positive integer user_id. Existing expiry and nbf validation remains in
force with no added leeway. All failures return nil claims and ErrInvalidToken;
HTTP responses remain generic. Reject before staff lookup/business work.

Current generators already emit this shape. Token field names, TTLs, JWT headers,
generation signatures, login/refresh response DTOs and current-DB authorization
stay unchanged. The token's role/email are informational; parsing does not grant
permissions or require a particular role claim. Even with equal access/refresh
secrets, the purpose check must prevent cross-use.

Explicitly out of scope: mandatory iss/aud/sub/iat/jti/session claims, key rings,
refresh rotation, password/session revocation, cookies/CSRF/CORS, hash policy,
auth error remapping and Laravel/mobile verification. New required claims need
a versioned generator/parser rollout and operator/client integration evidence;
do not impose claims that current tokens never minted. Noncanonical tokens
previously signed with another HMAC algorithm or without exp require re-login.

Acceptance: generated access/refresh round trips; HS384/HS512/none/wrong secret,
missing/null/invalid/expired exp, zero/negative/missing/non-integer identity,
future nbf, missing/wrong purpose and equal-secret cross-use all deny with nil
claims. Middleware must not look up staff or invoke a handler on invalid access;
refresh HTTP rejects malformed-policy tokens before database work. Existing
real-DB permission/mutation tests remain green. No claim of live deployment or
complete token/session security follows from this slice.
