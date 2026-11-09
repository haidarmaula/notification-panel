# JSON request body limits

M02d: implemented and locally verified 2026-11-03, not deployed.
[SPEC](../specs/m02d-json-request-limits.md), [evidence](../analysis/m02d-verification.md).

| Limit (inclusive, raw bytes) | Routes |
| --- | --- |
| 65,536 (64 KiB) | POST `/api/v1/auth/login`, POST `/api/v1/auth/refresh`, POST `/api/v1/auth/logout` |
| 65,536 | POST `/api/v1/mobile/sync` |
| 65,536 | PATCH `/api/v1/profile`, PATCH `/api/v1/profile/password` |
| 65,536 | POST `/api/v1/staff`, PATCH `/api/v1/staff/{id}`, PATCH `/api/v1/staff/{id}/status`, PATCH `/api/v1/staff/{id}/password` |
| 65,536 | POST `/api/v1/segments`, PATCH `/api/v1/segments/{id}`, POST `/api/v1/segments/{id}/members` |
| 65,536 | POST `/api/v1/device-tokens`, PATCH `/api/v1/device-tokens/{id}` |
| 1,048,576 (1 MiB) | POST `/api/v1/notifications`, PATCH `/api/v1/notifications/{id}` |

The limit counts the complete body: UTF-8 bytes, unknown fields and trailing
whitespace. It applies even when Content-Length is absent. Larger bodies return
413 before field validation or business work, including a small valid object
followed by excessive whitespace. Existing outer route authentication/permission guards run
first; their 401/403 can therefore precede body errors. Legacy `/mobile/sync`
verifies its body JWT in the service after decoding; no outer guard is added here.

```json
{"success":false,"message":"request body too large"}
```

A body within budget must contain one complete JSON value. Empty/malformed
bodies, a second value or trailing garbage return 400 `invalid request body`.
This closes prior silent acceptance of a valid first value followed by data.
Unknown fields and duplicate-key decoding remain compatible; null still reaches
existing field validation. Content-Type checks are unchanged (no new 415).
Requests are not decompressed by this helper. Body-less operations keep their
existing behavior.

Clients should shrink over-budget requests before retrying; repeating identical
oversized input will still fail. Use broadcast/segment targeting for an audience
rather than sending its entire device list. These backend budgets are distinct
from provider payload limits. They do not establish field/item/depth limits,
rate limiting, timeouts, concurrent memory bounds or capacity. Future changes to
those controls need their own compatibility contract.
