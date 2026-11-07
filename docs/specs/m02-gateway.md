# Protected public API ingress

Production server policies require an `ingress` object. Deploy the gateway
configuration and client certificate before switching images. The gateway accepts
external HTTPS and connects to the application's private listener using TLS 1.3
and its dedicated client certificate. Restrict the CA to these gateways. The
gateway must independently authenticate and authorize any caller for whom it
injects the server-owned API key. Browsers and mobile apps must never receive
that shared secret.

```json
{
  "ingress": {
    "endpoint": {
      "listen": "10.0.0.20:8080",
      "server_certificate_pem": "<server certificate chain>",
      "server_key_pem": "<server private key>",
      "client_ca_pem": "<dedicated gateway client CA>"
    },
    "gateway_ips": ["10.0.0.10"],
    "ip_per_minute": 120,
    "auth_ip_per_minute": 10,
    "auth_total_per_minute": 60,
    "concurrent_requests": 8
  }
}
```

Use the instance's assigned private IP or loopback address. Each TLS purpose
has separate certificates and client trust. Scraper certificates must have no
authority on the business listener. Grant network access to the configured
gateway peers and remove direct access to the development listener.
`--deployment` rejects a missing ingress policy. The environment-based local
command keeps its existing development interface.

The API checks the connected socket peer against up to 16 exact addresses on
every request. The gateway must overwrite X-Forwarded-Proto with `https` and
X-Forwarded-For with one canonical IPv4 or IPv6 address. Duplicate headers,
address chains, alternate schemes, missing TLS and unapproved peers fail before
credential parsing. Staff audit attribution uses the same verified address.
The receipt project's proxy settings must match the gateway that serves it.

Configure the example rate and concurrency values for each deployment. A fixed
minute window counts requests per verified IP. POST handlers under /api/v1/auth
also consume per-IP and aggregate authentication budgets before a password
hash or token exchange. Their limits cover login, refresh and logout. Each
window tracks at most 65,536 keys. Each request limit is between 1 and 60,000
per minute. A rejected request receives 429 plus the seconds to the next window.
A full process concurrency limit (1–1,024 admitted requests) returns 503 with
Retry-After 1. Responses use Cache-Control no-store. Configure gateway-side
connection, body, slow-client, distributed account abuse and fleet-wide budgets
for the real topology. End-of-minute bursts and additional replicas can multiply
the effective rate. Protected API headers are limited to 32 KiB plus the Go
HTTP server's fixed parsing allowance.

Run the ingress/config race tests and the integration suite. The latter uses
real client/server certificates to verify denial before routing, successful
gateway authentication and staff audit attribution, then forces the same
client over the exchange limit. Certificate rotation, BFF identity, configured
peer addresses and a fleet attack drill require target-environment evidence.
