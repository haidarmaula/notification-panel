# Application scope — proposed API contract

2026-11-03. **Design only: no app-scoped endpoints or isolation implemented.**
[ADR-12](../adr/adr-12-multi-application-isolation.md),
[M03 sequence](../specs/m03a-application-scope-plan.md).

Modern application-owned resources will use explicit application context, for
example `/api/v1/apps/{app_id}/notifications` and
`/api/v1/apps/{app_id}/segments`. These are proposed namespace examples, not
currently callable endpoints. Exact lifecycle/control-plane methods and DTOs
are specified per implementation slice.

| Concern | Proposed caller-visible contract |
| --- | --- |
| Application selector | Immutable opaque app ID resolved by backend; client cannot override it using body/header/query fields |
| Staff business access | Existing operation role permission AND current active DB app grant; token role/app strings are not authority |
| Global control plane | Staff directory/profile/auth separate; SUPER_ADMIN application/grant administration policy remains proposed |
| Mobile identity | Configured per-app issuer/audience/purpose and app-origin/proof contract; no mandatory Laravel/React Native implementation |
| User subject | Same external subject in apps A/B maps to distinct notification users by default |
| BROADCAST | Eligible devices only within the selected app |
| Resource references | Segment/user/template/binding/delivery must belong to the same app; app ownership is immutable |
| Missing/ungranted app or foreign resource | Uniform not-found; no cross-app existence oracle |
| Missing/invalid authentication | 401; body/JWT strings must never be echoed |
| Insufficient operation role within a granted app | 403 |
| Authority/internal failure | Generic 500, fail closed |
| Replay/idempotency/receipt | Preserve original app/deployment/resource context; keys and proof from app A cannot affect app B |
| No app context | Deny; no implicit first/default app or global fan-out |

The current unscoped API and M02 role matrix remain the implemented legacy
contract. They do NOT establish multi-application safety. Introducing app B's
business data is gated on retiring global legacy routes or an explicitly tested
server-bound single-app bridge, covering reads/search/counts as well as writes. Old worker/scheduler/fan-out
processes must also be scoped or drained/stopped before app B data appears.
Do not add an optional app_id to the current API and call that isolation.

FCM project/credential resolution is server-owned; no request chooses a credential
file, arbitrary project, SDK client or provider endpoint. Body limits and provider
payload/target limits are separate controls. Future DTOs must retain safe errors,
explicit presence/idempotency semantics and the existing no-credential audit rule.
