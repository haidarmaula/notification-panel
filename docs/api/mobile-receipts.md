# Mobile device receipts

`POST /api/v1/apps/{app_id}/mobile/notifications/{campaign_id}/receipt`
requires the configured project's mobile bearer JWT over TLS. Send the same
client-generated UUID when retrying an uncertain response. Preserve the
original application, account and capability when queueing an offline report.

```json
{
  "delivery_id": "8c46ed8c-f1d2-4916-91ab-054a31d2e2ea",
  "client_event_id": "60ab83aa-fc95-449d-a08e-5bed3ef30410",
  "event": "OPENED",
  "receipt_token": "<opaque value received in this device's push payload>",
  "observed_at": "2026-11-08T02:00:00Z"
}
```

Use `application/json`, a maximum of 4096 bytes, canonical lower-case UUIDs,
and either DELIVERED or OPENED. The optional RFC 3339 client time is metadata.
It must fall within the delivery creation and capability expiry window, with
up to five minutes of clock skew, and at most five minutes ahead of the server.
Server receipt time sets the first observation timestamp. OPENED also creates
one user read. A failed submission can still have a late device observation.

| HTTP status | Client action |
| --- | --- |
| 200 | Remove the acknowledged report. This includes exact and semantic duplicates. |
| 400, 413, 415 | Correct the input, size or media type. |
| 401 | Refresh the original account's session before retrying. |
| 403 | Use the configured secure transport. |
| 404 | Unknown, foreign, reassigned or invalid capability. Discard the report. |
| 409 | The event ID already belongs to a different report, or the project is inactive. |
| 410 | The authenticated owner's capability expired. Discard the report. |
| 429 | Wait the stated Retry-After seconds. |
| 500, 503 | Retry the same report with backoff. |

## Server policy

A project in the mobile policy file opts in with a `receipts` object.
Set `environment` and `verification_keys` (entries with `id` and base64 public
`key`) to match the worker's receipt key ring. Keep deployment environments
and key purposes separate. The server needs only the public receipt keys.
Set `ip_per_minute`, `principal_per_minute` and `installation_per_minute`
between 1 and 60000. Limits apply per process. Budget for every replica and
set the deployment's upstream abuse controls accordingly.

Direct TLS uses the socket peer address. For TLS termination, list the proxy's
exact peer addresses in `trusted_tls_proxies`. Require the proxy to overwrite
X-Forwarded-Proto and X-Forwarded-For with the verified scheme and a single
client IP. Deny direct access to the origin on that network. Use an empty list
for direct TLS. Never log bearer credentials, receipt tokens or raw request bodies.
