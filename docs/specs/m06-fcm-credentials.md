# M06 protected FCM file credentials

Date: 2026-11-05. Status: implemented; local verification recorded separately. Delivery stays disabled.
Related: [transport](m06-fcm-transport.md), [ADR-04](../adr/adr-04-provider-selection.md#a-2-credential-source-and-startup-evidence).

## Construction and identity

The explicit file factory accepts an immutable transport config plus a protected
secret path, expected credential project and service-account email. It uses the
same root-owned, canonical-path, non-symlink, safe ancestry/mode/ACL and 64 KiB
checks as deployment policy. Secrets are mounted read-only by deployment; the
application cannot establish mount provenance by parsing JSON. Never read a key
selected by an HTTP request, environment override or developer ADC discovery.

Parse only a service-account JSON object: reject duplicate/case-aliased/unknown
fields, missing required fields, nulls and trailing data. Match the credential
project/email exactly to policy; target project is independently explicit in
transport config. Different credential/target projects require explicit reviewed
cross-project approval. Do not assume a home-project match proves IAM permission.
Allow standard informational Google key fields without following their URLs.
Pin token_uri to https://oauth2.googleapis.com/token and the Google universe.
Require one RSA PKCS8/PKCS1 PEM key, 2048–4096 bits and valid private structure;
reject other algorithms, DER over 4 KiB or trailing/prefixed PEM material. No key JSON or raw errors are
logged or returned. Construction validates locally and sends no network probe.
Caller cancellation is checked around bounded local reading/parsing; it cannot
interrupt a stalled kernel read on an unsuitable secret filesystem.

Use the pinned cloud auth library's 2LO provider to sign the assertion. Request
only https://www.googleapis.com/auth/firebase.messaging, no domain delegation,
custom claims or self-signed bearer fallback. Library logging is explicitly
silenced even when Google SDK debug environment is set. Token requests have a
fixed HTTPS destination, verified frozen system roots, no proxy/redirect and
bounded headers/body/time/connections. OAuth errors never expose server bodies.

## Refresh and shutdown

Refresh is synchronous under a cancellable single-flight gate; no async refresh
or detached timeout goroutine. Cached tokens are copied and reused only while
more than a minute remains. One bounded exchange supplies concurrent callers;
a waiting caller can cancel independently. Validate Bearer, nonempty bounded
ASCII token and expires_in in 1–3600 seconds before SDK parsing/duration math.
No ID-token response can override expiry. Returned tokens must cover the send
request timeout. A failed refresh returns a safe credential error, never an
expired token. Client Close cancels/joins sends including refresh/gate waiters,
then closes both credential and FCM idle connections. An unsupported ACL-inspection filesystem (including the tested Docker tmpfs) is
refused; choose a secret mount whose ACLs can be inspected, rather than disabling
the guard. Rotation constructs a new
client from reviewed policy and drains/closes the old one; files are not reread
or hot-swapped during sends.

## Scope and remaining gate

This implements ADR-04's file mode first. The generic transport still accepts a
trusted context-aware TokenProvider for future keyless composition. ADC/workload
identity discovery/provenance is not yet deployed: hosting/identity is unverified,
and ambient gcloud/user credentials must not silently become worker authority.
This boundary does not remove ADC from the intended supported credential modes.
A reviewed managed-identity factory remains required before selecting ADC.

Startup construction does not prove OAuth validity, target IAM permission, quota
or APNs/mobile readiness. An authorized test-project/device preflight and least
privilege review remain release gates. No real credential is needed for tests.
Local simulated OAuth tests exercise library assertion serialization/refresh,
then the actual SDK send; they do not prove Google's acceptance of a real key.

Primary references (checked 2026-11-05):
[Google service-account OAuth](https://developers.google.com/identity/protocols/oauth2/service-account)
and pinned cloud auth v0.20.0 `auth.go` / `credentials/filetypes.go`. The high-level
file factory wraps its provider with default async caching; this implementation
uses its supported 2LO provider directly and owns synchronous cancellation-aware
caching instead of assuming DetectOptions disables that wrapper's background work.

Verification: [file credential evidence](../analysis/m06-fcm-credentials-verification.md).
