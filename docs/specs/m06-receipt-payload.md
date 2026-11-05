# M06/M07 receipt capability and FCM payload

Date: 2026-11-05. Status: signer/verifier, payload builder and atomic metadata
freeze are implemented and locally tested. Transport/composition remain open;
no delivery or receipt route is enabled. See [verification](../analysis/m06-receipt-payload-verification.md).
Related: [M06](m06-execution.md), [ADR-05](../adr/adr-05-receipt-ingestion.md).

## Capability version 1

Use dedicated Ed25519 signing keys: worker receives private keys, receipt server
receives public keys only. This specifies ADR-05's unresolved signature encoding;
it does not reuse Laravel/staff/replay keys. Asymmetric verification avoids giving
the receipt server signing authority. Deployment owners must still enforce purpose
and environment separation when distributing keys; constructors cannot prove it.

Encoding is `r1.<key-id>.<payload>.<signature>`. Key ID is 1–32 ASCII letters,
digits, underscore or hyphen. Payload/signature use canonical unpadded base64url.
Payload is exactly 96 bytes, in this order:

| Field | Bytes | Encoding |
| --- | --- | --- |
| application public UUID | 16 | UUID network bytes |
| immutable business origin epoch | 16 | UUID network bytes |
| notification ID | 8 | positive int64, unsigned big endian |
| opaque delivery UUID | 16 | UUID network bytes |
| immutable binding ID | 8 | positive int64, unsigned big endian |
| installation public UUID | 16 | UUID network bytes |
| owner epoch | 8 | positive int64, unsigned big endian |
| expiry Unix seconds | 8 | positive int64, unsigned big endian |

Sign bytes `hello/receipt-capability/v1\x00` + environment + `\x00` + the first
three dot-separated token segments. Environment is a pinned 1–64 character ASCII
label (letters, digits, underscore, hyphen); it is never selected by a token.
Algorithm/version are fixed. No JWT algorithm negotiation or key URL exists.
Require nonzero UUIDs, positive IDs/epoch and expiry through year 9999. Reject
unknown key IDs, malformed/oversized/noncanonical encodings and mismatched fields
with one safe invalid-capability error. Verify the signature and exact persisted
scope before returning an expiry distinction; expiry is exclusive (`now >= exp`).

Key rings are bounded to 1–16 entries and defensively copied. Public keys are
screened for noncanonical y coordinates and all small-order encodings, including
negative-zero aliases. Remaining point/signature validation uses the Go standard
library; this screen does not establish full subgroup membership or provenance. Sign with an explicit
persisted key ID, never whichever key happens to be newest on a retry. Ed25519 is
deterministic: identical persisted scope/key/environment gives identical bytes.
Keep old public keys through their approved observation window; removing a key
revokes its tokens. Rotation must retain the private key while an accepted pending
delivery may still need its deterministic payload. No implicit fallback key.

The token is signed, not encrypted; its encoded metadata is not confidential. It
contains no user PII, raw address or credentials. Treat the whole value as secret
payload-possession capability: ordinary formatting, structured logs and JSON
serialization of the opaque Go value redact it. Only an explicit transport method
reveals its wire string. Never expose it in admin reads, analytics or arbitrary
mobile delivery listing.

Cryptographic validity is not mobile authorization. M07 must authenticate the
mobile principal and lock/recheck original delivery/binding/current installation
owner epoch before accepting evidence. Same-owner refresh may report an old push;
logout/account transfer cannot. Signature verification alone must not write an
observation. Semantic dedup and idempotency remain M07 work.

## Committed signing metadata

Migration 000029 adds immutable per-delivery metadata: capability version, key ID,
environment and expiry Unix seconds. A composite application/delivery FK prevents
cross-application attribution; identity comes from immutable intent/preparation/
installation/application rows rather than copied mutable request fields.

`ExecutionPolicy` requires explicit `receipt.Policy`: environment/key ID and a
whole-second observation window (1s–90d defensive bounds). There is no production
default; the proposed seven-day window still needs approval. Validate signer key
availability/overlap separately before enabling provider execution.

The first successful device reservation locks the delivery after its binding,
freezes metadata and charges the attempt in one transaction. Expiry is
ceil(accepted deadline Unix seconds) + the explicit observation window, bounded
through year 9999. Failed/uncertain commits return no signing material through the
reservation. Uncertainty may leave both metadata and charge durable, so no blind
retry recreates execution permission.

Existing metadata is never overwritten on retry, takeover or key/window rotation.
Return its original key/expiry for signing; missing overlap keys fail closed.
A foreign environment, unsupported version or expiry not covering the current
persisted deadline holds execution. This coverage check is not a general detector
of every possible deadline mutation; accepted-deadline immutability still follows
the campaign contract. No attempt count is refunded or audience identity changed.

An older charged intent without metadata is held for explicit reconciliation;
it is never silently backfilled. Trigger guards reject metadata update/delete,
and down refuses retained metadata. Retention/erasure must introduce a separately
reviewed maintenance protocol, not delete/recreate a new capability window.

An acknowledged `AttemptReservation.ReceiptMaterial` returns value-copied signing
inputs only. `Signer.SignMaterial` checks supported version and pinned environment
and uses the frozen key ID. This accessor is not provider execution permission.
The worker remains admission-only; transport/results/recovery/outbox are later work.

## FCM payload version 1

A builder is pinned to one application, deployment environment and trusted
provider route. Each request must exactly match route namespace/key/platform/
address type and capability scope. Destination binding ID, installation public
UUID and owner epoch must also equal the signed scope, preventing accidental
address/capability pairing across two bindings on the same route. Use precisely one target: Message.Fid for
FCM_FID, Message.Token for FCM_REGISTRATION_TOKEN. Android and iOS are supported;
raw APNs and OneSignal addresses are rejected. Credentials/project selection
belong to the later route-aware transport, not arbitrary request fields.

Data string fields are `schema_version=1`, `application_id`, `notification_id`,
`delivery_id`, `registration_id`, `owner_epoch`, and `receipt_token`. Custom
string data is copied, cannot overwrite these fields, and rejects FCM-reserved
keys/prefixes (`from`, `message_type`, `google*`, `gcm*`). At most 100 custom
fields are accepted. Title/body and data must be valid UTF-8. No raw registration address is
inserted into data. Title/body provide the user-visible notification; media,
quiet hours and display/tap behavior are separate consumer contracts.

Build only before the persisted deadline, with a capability expiry covering that
deadline. Android TTL is the remaining whole seconds rounded down (zero means
immediate-only for the final subsecond); iOS apns-expiration is the deadline's Unix
second, also rounded down. Neither extends the accepted freshness window. Reject
remaining lifetime above 28 days. These are provider queue-retention settings,
not a promise of device display before deadline.

Use a conservative local limit: the UTF-8 JSON-encoded message without its target
address must fit 4096 bytes, including notification/config/capability overhead.
This may reject some messages FCM would accept and is not an exact reproduction
of provider accounting. Validate again at provider submission; invalid payload
never proves an invalid registration. No truncation is permitted.

Primary sources, checked 2026-11-05:
[FCM lifespan](https://firebase.google.com/docs/cloud-messaging/customize-messages/setting-message-lifespan),
[FCM errors](https://firebase.google.com/docs/cloud-messaging/error-codes), and
pinned Firebase Admin Go v4.21.0 `messaging.Message`/Android/APNs serializers.
Real React Native Android/iOS payload handling and SDK addressing remain release
gates; SDK construction or local payload tests do not establish real delivery.
