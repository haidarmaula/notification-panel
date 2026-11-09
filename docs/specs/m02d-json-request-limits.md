# M02d — Bounded JSON request bodies

Contract established 2026-11-03. Local implementation/evidence is tracked in
[verification](../analysis/m02d-verification.md).

All 17 current handlers that decode JSON share a bounded reader/decoder:

- 65,536 bytes (64 KiB): auth login/refresh/logout, mobile sync, profile update/password,
  staff create/update/status/password, segment create/update/add member, device
  token register/update.
- 1,048,576 bytes (1 MiB): notification create/update, allowing bounded target lists.

These are initial backend safeguards, not provider payload limits or capacity
benchmarks. Broadcast/segment requests do not enumerate all recipient devices.
Large individual lists must fit the campaign request budget; no UPLOAD API is added.

The entire body, including unknown fields and trailing whitespace, counts as
bytes before JSON parsing or service calls. Exact-limit bodies are allowed;
limit+1 returns HTTP 413 with the existing envelope and safe message
`request body too large`. Content-Length can reject early but is never trusted
to bound reads: unknown/chunked or inaccurate smaller lengths still use the
bounded reader. Oversized malformed bodies also return 413 if the declared/read
size exceeds the limit. No body fragments or decoder errors are returned.

Bodies within budget must contain exactly one JSON value. Invalid/empty bodies,
trailing garbage or multiple values return existing HTTP 400 `invalid request body`.
Unknown fields, duplicate-key semantics, JSON null and current field validations
remain as before. This intentionally closes the previous acceptance of a first
valid JSON value followed by extra data. The M02d body-budget slice did not
change Content-Type handling. A separate M43 follow-up requires one
`application/json` Content-Type (optional `charset=utf-8`) and identity content
encoding for all mounted JSON request handlers; unsupported or compressed
requests return 415. See the [auth API contract](../api/auth-api.md),
[staff API](../api/staff-api.md) and [profile API](../api/profile-api.md).
Retired, unmounted legacy handlers retain their historical media-type behavior.

Existing route authentication/authorization executes first; body limits do not
replace those guards. Direct handler calls also enforce limits (existing identity
or path validation may run first). Services, repositories, audits and Kafka must
not run for a rejected body. Methods that do not decode bodies retain their
existing behavior; no global body prebuffer is introduced.

Verification: exact/over boundaries, UTF-8 byte counts, known/unknown/inaccurate
length, bounded read counts, oversized valid-prefix/trailing-data inputs,
17 direct handlers, authenticated real-module routes and unchanged DB/Kafka
snapshots on rejection. Staff/profile name and email additionally enforce the
schema-backed 255-character column bound. Request rate/time/header/depth and
other field-specific limits, total concurrent memory limits and ingress
configuration remain separate work.
