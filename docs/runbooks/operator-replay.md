# Restricted operator replay

Status: executable and session wiring tested locally on 2026-11-05. Production
activation, trusted MFA issuer/grant provisioning, source-incarnation approval,
backpressure/handoff exercises and deployment ACLs remain release gates.
See the [command contract](../specs/m05-replay-command.md) and
[verification](../analysis/m05-replay-command-verification.md).

## Deployment policy

Build with `go build ./cmd/requeue`. The binary accepts `--help` and `--execute`;
there is no config-path, actor, key, source-approval or insecure-transport option.
It reads `/etc/hello/requeue.json` on Linux, or the canonical
`/private/etc/hello/requeue.json` on macOS. It does not load `.env`.

Deployment automation must install the file as root, mode `0600` or `0640` with
an approved read-only runtime group. Every ancestor must be root-owned and
non-writable by group/others. Symlinks and extended ACLs are rejected. DB TLS
certificate/root/key paths have the same protected ancestry; public certificates
may be `0644`, while private keys must remain owner/group private. ACL inspection
must be supported by the filesystem; unsupported/error results fail closed.
Other operating systems currently cannot load deployment policy.

Only trusted operational hosts/accounts may read the database/SASL/private-key
material. Supply no nonempty `PG*`, `SSL_CERT_FILE` or `SSL_CERT_DIR` environment
settings: they are rejected rather than treated as a second trust source.

The configuration requires all fields in this template. Replace placeholders
through trusted secret/config provisioning; the template is not a working policy.

```json
{
  "database_url": "postgresql://replay:URL_ENCODED_SECRET@db.example.internal:5432/notifications?sslmode=verify-full",
  "issuer": "<dedicated-operational-issuer>",
  "audience": "<replay-audience>",
  "deployment": "<exact-deployment>",
  "verification_key": "<standard-base64-Ed25519-public-key>",
  "log_source_id": "<approved-source-UUID>",
  "cluster_identity": "<approved-cluster-ID>",
  "topic_name": "notification.send.requested",
  "topic_incarnation": "<reviewed-incarnation-label>",
  "partition_count": 1,
  "kafka": {
    "brokers": ["broker.example.internal:9093"],
    "authentication": "SASL_PLAIN",
    "ca_pem": "",
    "client_certificate_pem": "",
    "client_key_pem": "",
    "sasl_username": "<scoped-replay-principal>",
    "sasl_password": "<secret>"
  }
}
```

`partition_count` must match the actual approved topology; `1` is only an example,
not a production HA recommendation. Match source identity with the immutable,
active database registration and reviewed deployment manifest. Deployment must
remain unsealed. The command never registers/activates a source or changes epochs.

TLS 1.2+ and server identity verification are mandatory. An empty `ca_pem` uses
OS trust; otherwise provide PEM CA certificates inline. `SASL_PLAIN` transmits
credentials only inside verified TLS and requires empty client-certificate fields.
For `MTLS`, provide the client certificate chain and private key as inline PEM,
and leave both SASL fields empty. Anonymous TLS, plaintext and unsupported SASL
profiles are rejected; there is no fallback. Confirm real broker ACLs and the
selected authentication profile before release. Local positive broker evidence
currently covers mTLS; SASL/PLAIN configuration is unit-tested, not tested against
a real SASL listener.

## Execute an approved request

1. Obtain current staff send authority, an explicit application grant and a
   separate revocable operational replay grant through the approved channel.
2. Have the trusted MFA issuer sign a short-lived assertion bound to the complete
   application/request/reference/source-position selection. Ordinary staff JWTs
   and shell access do not provide replay authority.
3. Supply the request envelope through stdin using a protected input stream. See
   the command contract for the JSON schema. Do not put assertions in argv,
   shell history or ordinary incident logs.
4. Run `requeue < protected-input.json` for advisory inspection. Inspect every
   classification; this does not reserve claims or authorize later execution.
5. Run `requeue --execute < protected-input.json` only while the same assertion
   remains valid. Each position rechecks current DB authority, grant, state,
   source/epochs and broker metadata. A batch is serial, not atomic.

Use JSON lines on stdout as the safe result report and stderr for diagnostics.
Exit 0 means the batch completed with reported classifications, not device
arrival. `published` means broker acknowledgement only. Exit 1 means runtime,
authority, cancellation, output/shutdown or uncertain-publication failure;
exit 2 means invalid arguments/input. Logical held/rejected/no-op results can
continue; any failure/uncertainty stops later positions.

## Interruption and reconciliation

Retain the request UUID and earlier result rows. Do not generate a new request
identity or blindly retry after missing output, `started`, `publish_unconfirmed`,
`abandoned_unknown`, timeout, crash or shutdown failure. Reconcile persisted
request/attempt/control/audit evidence first; the same UUID supports safe readback
of known results but does not recreate a lost execution proof.

SIGINT/SIGTERM cancels work and interrupts incomplete stdin/blocked stdout.
The command stops new calls and drains its producer before closing the pool and
returning. A call timeout or released DB lock does not establish remote I/O drain.
Source/epoch handoff, topic recreation and restore require operator supervision
that confirms the prior process and producer have drained. Retired/recreated
sources require a separate reviewed workflow; the SDK cannot independently prove
a topic UUID from its metadata response.

Background QUEUED recovery is the normal recovery path. Expired replay-claim
cleanup is implemented but is not yet wired into the scheduler. Replay never
rebuilds an audience, resets attempt budgets, seeks offsets or calls FCM.
