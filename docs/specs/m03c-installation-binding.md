# M03c installation and binding contract

Status: proposed implementation contract. Production issuer, origin verification,
provider route and secret configuration require per-project evidence.
See [ADR-04](../adr/adr-04-provider-selection.md) and the
[application scope plan](m03a-application-scope-plan.md).

## Authority and storage

Resolve the public project UUID to a server-owned project and require ACTIVE
before business operations. Verify the mobile JWT against that project's pinned
issuer, audience, algorithm, key and token purpose. Resolve the verified subject
through the project's trusted identity namespace. A request cannot select a
provider project or override its authenticated user through the body. Enrollment
also requires the configured app-origin policy. Unconfigured policies fail closed.

An installation has a public opaque ID, immutable application ID, a verifier
for a cryptographically random 32-byte secret, nullable current owner, version
and owner epoch. Issue the secret only after successful creation. Compare proof
in constant time and keep proof and verifier out of logs, audit and DTOs. If the
enrollment response is lost, do not disclose or reissue the existing secret to a
caller who only knows the address.

Store modern binding generations separately from legacy device_tokens, whose
plaintext address and global uniqueness are legacy constraints. The modern
binding is the immutable dispatch identity referred to as a device-token binding
in ADR-04. A generation stores project, installation, owner, owner epoch and the
server-resolved provider route. Campaign admission and receipts must reference
this generation directly. Legacy rows remain historical input and require
re-enrollment before they can participate in the scoped pipeline.

Encrypt the provider address with authenticated encryption under a versioned
key. Bind ciphertext to project, installation and generation. Maintain a
keyed address fingerprint over the canonical provider namespace, address type
and exact address bytes. A partial unique constraint for active bindings must
cover provider namespace, address type and fingerprint across applications that
share that namespace. Another partial unique constraint permits at most one
active generation per installation. Fingerprint key replacement requires a
coordinated reindex with continuity of conflict checks, separate from encryption
key rotation. Neither raw address nor fingerprint belongs in staff projections.

## Transitions

| Operation | Conditions and atomic result |
| --- | --- |
| Enroll | Verified user and app origin. Create installation and first generation. Reject an already active address without revealing its owner. |
| Refresh | Same project, current user, proof and matching version. Exact address updates last-seen and supplied metadata only. Changed address retires the old generation and inserts a new one, increasing version while retaining owner epoch. |
| Logout | Same project, current user, proof and matching version. Retire current generation, clear owner and advance version and owner epoch. |
| Switch account | Verified destination user in the same project, proof and matching version. Retire old generation, set new owner, advance version and owner epoch and insert a new generation. |
| Provider invalidation | Compare the original generation and owner epoch. Retire only that generation if still current. Advance installation version so a stale refresh cannot overwrite the result. |
| Lost proof | Use independently enrolled address or a reviewed recovery flow. Address knowledge cannot transfer or reissue a binding. |

Proof or ownership failure returns the same not-found result as an unknown
installation. Check authority and proof before returning version conflicts.
A stale version or active address collision returns 409. Uncertain commits
require state reconciliation rather than blindly replaying a mutation. A
client that still has its proof may re-read the current state under the same
project and owner rules. The HTTP contract belongs to M03d.

Acquire locks in the shared CR1 order. Resolve and lock project/control
policy, then principal rows in stable ID order, then installation and current
generation. Recheck authority, proof and version in the transaction before
changing state. Enforce address ownership by unique constraints, including
concurrent enrollments under different projects. Retry whole transactions
only on serialization failures or deadlocks. Increment versions on changes
that affect lifecycle preconditions. An old delivery keeps its original
binding and owner epoch even after the installation changes.

Retain inactive generations while any delivery, pending receipt, retry or
recovery record references them. Restrict deletion and preserve revocation
evidence for the supported replay and receipt windows. Exact time limits and
safe purge order depend on M09. No automatic reclamation of an address by
deleting history is part of this slice.

## Implementation and acceptance

1. Add schema, scoped composite references and immutable generation guards.
2. Implement protected address/proof storage and transaction-bound lifecycle.
3. Verify per-project mobile authority and wire the lifecycle under its contract.
4. Test foreign project, stale proof/version, logout versus refresh, account
   switch versus invalidation, shared-provider address races and rollback after
   conflicting insert. Verify that historic delivery attribution survives and
   no token or proof appears in a response except the initial enrollment secret.
