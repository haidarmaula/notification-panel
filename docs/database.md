# Notification Backend Database Documentation

**Version:** 1.0  
**Last Updated:** 2026-07-23

---

## Overview

The Notification Backend Database stores and manages all data for the notification system, including users, device tokens, staff accounts, notification campaigns, segments, delivery records, and audit logs.

The system follows an event-driven architecture where notification requests are persisted in the database, published to Kafka, and processed asynchronously.

---

## High-Level Architecture

```text
Admin
    │
    ▼
Notification API
    │
    ▼
PostgreSQL
    │
    ▼
Kafka
    │
    ▼
Notification Worker
    │
    ▼
OneSignal
    │
    ▼
Mobile Application
```

---

## 1. Users

### Purpose

Stores all mobile application users.

Notifications are always targeted at users, and the system resolves all registered devices belonging to those users.

### Relationships

- One user can have many `device_tokens`
- One user can be a member of many `segment_members`
- One user can have many `notification_deliveries`
- One user can have many `notification_reads`

### Columns

| Column      | Type          | Description                                          |
| ----------- | ------------- | ---------------------------------------------------- |
| `id`        | BIGSERIAL     | Primary key                                          |
| `external_id` | VARCHAR(100) | Identifier from an external system (HRIS, ERP, etc.) |
| `name`      | VARCHAR(255)  | User's full name                                     |
| `email`     | VARCHAR(255)  | User email address                                   |
| `status`    | VARCHAR(30)   | `ACTIVE` or `INACTIVE`                               |
| `created_at` | TIMESTAMPTZ  | Record creation timestamp                            |
| `updated_at` | TIMESTAMPTZ  | Last update timestamp                                |

---

## 2. Device Tokens

### Purpose

Stores push notification tokens (OneSignal player IDs) for mobile devices.

Each user may sign in from multiple devices, each having its own push token. When processing notifications, the worker retrieves all active device tokens associated with the target users.

### Relationships

- Belongs to one `user`
- Has many `notification_deliveries`

### Columns

| Column          | Type          | Description                                    |
| --------------- | ------------- | ---------------------------------------------- |
| `id`            | BIGSERIAL     | Primary key                                    |
| `user_id`       | BIGINT        | FK to `users.id`                               |
| `provider`      | VARCHAR(30)   | `FCM`, `APNS`, or `HUAWEI`                     |
| `platform`      | VARCHAR(30)   | `ANDROID`, `IOS`, or `WEB`                     |
| `installation_id` | VARCHAR(255) | Optional installation identifier               |
| `push_token`    | TEXT          | OneSignal player ID (unique)                   |
| `app_version`   | VARCHAR(50)   | Application version                            |
| `os_version`    | VARCHAR(50)   | Operating system version                       |
| `device_model`  | VARCHAR(100)  | Device model name                              |
| `is_active`     | BOOLEAN       | Whether the token is still valid               |
| `last_seen_at`  | TIMESTAMPTZ   | Last time the token was used                   |
| `created_at`    | TIMESTAMPTZ   | Record creation timestamp                      |
| `updated_at`    | TIMESTAMPTZ   | Last update timestamp                          |

---

## 3. Roles

### Purpose

Defines administrator roles within the system.

### Relationships

- One role can be assigned to many `staff_users`

### Columns

| Column      | Type          | Description                  |
| ----------- | ------------- | ---------------------------- |
| `id`        | BIGSERIAL     | Primary key                  |
| `name`      | VARCHAR(100)  | Role name (unique)           |
| `description` | TEXT        | Optional role description    |
| `created_at` | TIMESTAMPTZ  | Record creation timestamp    |
| `updated_at` | TIMESTAMPTZ  | Last update timestamp        |

### Example Roles

| Name          | Description                    |
| ------------- | ------------------------------ |
| `SUPER_ADMIN` | Full system access             |
| `ADMIN`       | Administrative access          |
| `OPERATOR`    | Limited operational access     |

---

## 4. Staff Users

### Purpose

Stores staff / administrator accounts that can access the Web Admin portal.

This table is completely independent from the `users` table, since staff members are not mobile application users.

### Relationships

- Belongs to one `role`
- Created many `templates`
- Created many `segments`
- Created many `notifications`
- Created many `upload_batches`
- Has many `audit_logs` as actor

### Columns

| Column        | Type          | Description                                    |
| ------------- | ------------- | ---------------------------------------------- |
| `id`          | BIGSERIAL     | Primary key                                    |
| `role_id`     | BIGINT        | FK to `roles.id`                               |
| `name`        | VARCHAR(255)  | Staff full name                                |
| `email`       | VARCHAR(255)  | Staff email address (unique)                   |
| `password_hash` | TEXT        | Bcrypt hashed password                         |
| `is_active`   | BOOLEAN       | Whether the account is active                  |
| `created_at`  | TIMESTAMPTZ   | Record creation timestamp                      |
| `updated_at`  | TIMESTAMPTZ   | Last update timestamp                          |

---

## 5. Templates

### Purpose

Stores reusable notification templates with placeholders.

Before sending notifications, the worker replaces placeholders with actual user-specific values.

### Relationships

- Belongs to one `staff_user` (creator)
- Can be referenced by many `notifications`

### Columns

| Column          | Type          | Description                                    |
| --------------- | ------------- | ---------------------------------------------- |
| `id`            | BIGSERIAL     | Primary key                                    |
| `name`          | VARCHAR(255)  | Template name                                  |
| `title_template` | TEXT         | Title template with placeholders               |
| `body_template` | TEXT         | Body template with placeholders                |
| `variables`     | JSONB         | JSONB array describing available variables     |
| `created_by`    | BIGINT        | FK to `staff_users.id`                         |
| `is_active`     | BOOLEAN       | Whether the template is enabled                |
| `created_at`    | TIMESTAMPTZ   | Record creation timestamp                      |
| `updated_at`    | TIMESTAMPTZ   | Last update timestamp                          |

### Example

**Title Template:** `Hello {{name}}`  
**Body Template:** `Your order {{order_number}} has been shipped.`

---

## 6. Segments

### Purpose

Represents logical groups of users that can receive notifications collectively.

### Relationships

- Belongs to one `staff_user` (creator)
- Has many `segment_members`
- Can be referenced by many `notification_targets`

### Columns

| Column      | Type          | Description                         |
| ----------- | ------------- | ----------------------------------- |
| `id`        | BIGSERIAL     | Primary key                         |
| `name`      | VARCHAR(255)  | Segment name                        |
| `description` | TEXT        | Optional segment description        |
| `created_by` | BIGINT        | FK to `staff_users.id`              |
| `created_at` | TIMESTAMPTZ   | Record creation timestamp           |
| `updated_at` | TIMESTAMPTZ   | Last update timestamp               |

### Example Segments

```text
Jakarta
Premium Members
Gold Members
Powerlifting Club
Marketing Team
```

---

## 7. Segment Members

### Purpose

Defines the relationship between segments and users.

### Relationships

- Belongs to one `segment`
- Belongs to one `user`

### Columns

| Column      | Type          | Description                     |
| ----------- | ------------- | ------------------------------- |
| `id`        | BIGSERIAL     | Primary key                     |
| `segment_id` | BIGINT        | FK to `segments.id`             |
| `user_id`   | BIGINT        | FK to `users.id`                |
| `created_at` | TIMESTAMPTZ   | Record creation timestamp       |

### Unique Constraint

The combination `(segment_id, user_id)` is unique.

---

## 8. Notifications

### Purpose

Represents a notification campaign.

### Relationships

- Belongs to one `staff_user` (creator)
- Optionally belongs to one `template`
- Has many `notification_targets`
- Has many `notification_deliveries`
- Has many `notification_reads`

### Columns

| Column        | Type          | Description                                    |
| ------------- | ------------- | ---------------------------------------------- |
| `id`          | BIGSERIAL     | Primary key                                    |
| `template_id` | BIGINT        | FK to `templates.id` (nullable)                |
| `template_name` | VARCHAR(255) | Snapshot of template name at creation time     |
| `title`       | TEXT          | Notification title (may be rendered)           |
| `body`        | TEXT          | Notification body (may be rendered)            |
| `payload`     | JSONB         | Additional metadata for the app                |
| `priority`    | VARCHAR(20)   | `NORMAL` or `HIGH`                             |
| `status`      | VARCHAR(30)   | `DRAFT`, `SCHEDULED`, `QUEUED`, `PROCESSING`, `COMPLETED`, `FAILED`, `CANCELLED` |
| `scheduled_at` | TIMESTAMPTZ  | When the notification should be sent           |
| `published_at` | TIMESTAMPTZ  | When it was actually sent                      |
| `completed_at` | TIMESTAMPTZ  | When delivery processing finished              |
| `created_by`  | BIGINT        | FK to `staff_users.id`                         |
| `created_at`  | TIMESTAMPTZ   | Record creation timestamp                      |
| `updated_at`  | TIMESTAMPTZ   | Last update timestamp                          |

### Campaign Lifecycle

```text
DRAFT
    │
    ▼
SCHEDULED (if scheduled_at is set)
    │
    ▼
QUEUED (after admin clicks "Send")
    │
    ▼
PROCESSING (worker is processing)
    │
    ├──────────┐
    ▼          ▼
COMPLETED   FAILED
```

| Status       | Description                                    |
| ------------ | ---------------------------------------------- |
| `DRAFT`      | Editable; not yet sent                         |
| `SCHEDULED`  | Waiting for scheduled time                     |
| `QUEUED`     | Published to Kafka, waiting for worker         |
| `PROCESSING` | Worker is processing delivery                  |
| `COMPLETED`  | Successfully sent to all targets               |
| `FAILED`     | Processing failed (permanent)                  |
| `CANCELLED`  | Cancelled by admin                             |

---

## 9. Upload Batches

### Purpose

Stores metadata for Excel uploads used in notification targeting.

### Relationships

- Belongs to one `staff_user` (uploader)
- Has many `upload_batch_rows`
- Can be referenced by many `notification_targets`

### Columns

| Column            | Type          | Description                                    |
| ----------------- | ------------- | ---------------------------------------------- |
| `id`              | BIGSERIAL     | Primary key                                    |
| `uploaded_by`     | BIGINT        | FK to `staff_users.id`                         |
| `original_filename` | VARCHAR(255) | Original filename of the uploaded file         |
| `total_rows`      | INTEGER       | Total rows in the file                         |
| `valid_rows`      | INTEGER       | Rows that passed validation                    |
| `invalid_rows`    | INTEGER       | Rows that failed validation                    |
| `created_at`      | TIMESTAMPTZ   | Record creation timestamp                      |

---

## 10. Upload Batch Rows

### Purpose

Stores validation results for every row within an uploaded Excel file.

### Relationships

- Belongs to one `upload_batch`

### Columns

| Column        | Type          | Description                                    |
| ------------- | ------------- | ---------------------------------------------- |
| `id`          | BIGSERIAL     | Primary key                                    |
| `batch_id`    | BIGINT        | FK to `upload_batches.id`                      |
| `external_id` | VARCHAR(100)  | Identifier from the uploaded file              |
| `is_valid`    | BOOLEAN       | `true` if validation passed                    |
| `error_message` | TEXT        | Reason for failure (if any)                    |
| `created_at`  | TIMESTAMPTZ   | Record creation timestamp                      |

---

## 11. Notification Targets

### Purpose

Defines the recipients of a notification campaign.

Instead of storing recipient information directly inside the `notifications` table, all targets are stored separately to support multiple targeting strategies.

### Relationships

- Belongs to one `notification`
- Optionally belongs to one `segment`
- Optionally belongs to one `user`
- Optionally belongs to one `upload_batch`

### Columns

| Column           | Type          | Description                                    |
| ---------------- | ------------- | ---------------------------------------------- |
| `id`             | BIGSERIAL     | Primary key                                    |
| `notification_id` | BIGINT       | FK to `notifications.id`                       |
| `target_type`    | VARCHAR(20)   | `GLOBAL`, `SEGMENT`, `USER`, or `UPLOAD`       |
| `segment_id`     | BIGINT        | FK to `segments.id` (if `target_type = SEGMENT`) |
| `user_id`        | BIGINT        | FK to `users.id` (if `target_type = USER`)     |
| `upload_batch_id` | BIGINT       | FK to `upload_batches.id` (if `target_type = UPLOAD`) |
| `created_at`     | TIMESTAMPTZ   | Record creation timestamp                      |

### Target Types

| Target Type | Description                                      |
| ----------- | ------------------------------------------------ |
| `GLOBAL`    | All active users                                 |
| `SEGMENT`   | Users belonging to a specific segment            |
| `USER`      | Specific individual users                        |
| `UPLOAD`    | Users imported from an Excel file                |

### Constraint

The `CHECK` constraint ensures exactly one of `segment_id`, `user_id`, or `upload_batch_id` is set according to `target_type`.

---

## 12. Notification Deliveries

### Purpose

Tracks the delivery status of notifications for every targeted user.

After consuming a Kafka event, the Notification Worker creates one delivery record per user.

### Relationships

- Belongs to one `notification`
- Belongs to one `user`
- Belongs to one `device_token`

### Columns

| Column              | Type          | Description                                    |
| ------------------- | ------------- | ---------------------------------------------- |
| `id`                | BIGSERIAL     | Primary key                                    |
| `notification_id`   | BIGINT        | FK to `notifications.id`                       |
| `user_id`           | BIGINT        | FK to `users.id`                               |
| `device_token_id`   | BIGINT        | FK to `device_tokens.id`                       |
| `provider`          | VARCHAR(20)   | `FCM`, `APNS`, or `HUAWEI`                     |
| `provider_message_id` | VARCHAR(255) | Identifier returned by the provider            |
| `status`            | VARCHAR(20)   | `PENDING`, `SENT`, `DELIVERED`, `OPENED`, or `FAILED` |
| `retry_count`       | INTEGER       | Number of retry attempts                       |
| `failed_reason`     | TEXT          | Reason if `status = FAILED`                    |
| `sent_at`           | TIMESTAMPTZ   | When the message was sent to the provider      |
| `delivered_at`      | TIMESTAMPTZ   | When the device confirmed delivery             |
| `opened_at`         | TIMESTAMPTZ   | When the user opened the notification          |
| `created_at`        | TIMESTAMPTZ   | Record creation timestamp                      |
| `updated_at`        | TIMESTAMPTZ   | Last update timestamp                          |

### Delivery Status Flow

```text
PENDING
    │
    ▼
SENT (sent to OneSignal)
    │
    ├──────────┐
    ▼          ▼
DELIVERED   FAILED
    │
    ▼
OPENED
```

| Status      | Description                                    |
| ----------- | ---------------------------------------------- |
| `PENDING`   | Delivery record created, not yet attempted     |
| `SENT`      | Sent to OneSignal, awaiting delivery confirmation |
| `DELIVERED` | Delivered to the device                        |
| `OPENED`    | User opened the notification                   |
| `FAILED`    | Delivery failed permanently                    |

---

## 13. Notification Reads

### Purpose

Records when users open notifications.

This information is used for analytics such as read rate, open rate, and user engagement.

### Relationships

- Belongs to one `notification`
- Belongs to one `user`

### Columns

| Column          | Type          | Description                                    |
| --------------- | ------------- | ---------------------------------------------- |
| `id`            | BIGSERIAL     | Primary key                                    |
| `notification_id` | BIGINT       | FK to `notifications.id`                       |
| `user_id`       | BIGINT        | FK to `users.id`                               |
| `read_at`       | TIMESTAMPTZ   | When the user opened the notification          |

### Unique Constraint

The combination `(notification_id, user_id)` is unique.

---

## 14. Audit Logs

### Purpose

Records all administrative actions performed through the Web Admin portal.

### Relationships

- Belongs to one `staff_user` (actor)

### Columns

| Column        | Type          | Description                                    |
| ------------- | ------------- | ---------------------------------------------- |
| `id`          | BIGSERIAL     | Primary key                                    |
| `actor_user_id` | BIGINT       | FK to `staff_users.id`                         |
| `action`      | VARCHAR(100)  | Action performed (e.g., `STAFF_CREATE`, `NOTIFICATION_DELETE`) |
| `entity_type` | VARCHAR(100)  | Type of affected entity (e.g., `staff_user`, `notification`) |
| `entity_name` | VARCHAR(255)  | Human-readable entity name                     |
| `entity_id`   | BIGINT        | ID of the affected entity                      |
| `before_json` | JSONB         | JSON snapshot before the change                |
| `after_json`  | JSONB         | JSON snapshot after the change                 |
| `ip_address`  | VARCHAR(100)  | IP address of the actor                        |
| `user_agent`  | TEXT          | User agent string                              |
| `created_at`  | TIMESTAMPTZ   | Record creation timestamp                      |

### Staff snapshot contract (M02c)

New staff audit JSON uses only ID, RoleID, optional RoleName, Name, Email,
IsActive, CreatedAt and UpdatedAt, preserving existing field casing. Password
hashes/credentials are excluded at the feature boundary; AuditService rejects
raw staff rows/maps. Password-reset events have SQL NULL before/after snapshots.
Name/email remain intentional audit PII. Other entity serialization is unchanged.

No schema change or sqlc regeneration is needed: the existing JSONB columns
remain. Historical credential removal is an explicit bounded data operation,
separate from automatic migrations, because it changes persisted audit state
and cannot reconstruct removed credentials after commit. See
[SPEC](specs/m02c-staff-audit-redaction.md) and
[operator procedure](runbooks/staff-audit-redaction.md). Local verification is
not evidence that production history, replicas or backups have been cleaned.

### Example Actions

| Action                    | Description                                    |
| ------------------------- | ---------------------------------------------- |
| `STAFF_CREATE`            | A new staff user was created                   |
| `STAFF_UPDATE`            | A staff user was updated                       |
| `STAFF_DELETE`            | A staff user was deleted                       |
| `SEGMENT_CREATE`          | A new segment was created                      |
| `NOTIFICATION_SEND`       | A notification was sent                        |

---

## Multi-application design checkpoint (2026-11-03)

Current tables still lack application isolation. [ADR-12](adr/adr-12-multi-application-isolation.md)
proposes an application registry, scoped external identities, app-qualified
business aggregates/children and composite reference constraints; global staff
control-plane data remains separate. [M03a–d](specs/m03a-application-scope-plan.md)
will define scoped backfill/constraints before sqlc generation.
The [M03a registry SPEC](specs/m03a-application-registry.md) now specifies
applications/grants plus nullable audit application_id and PROVISIONING-only status.
Migration 000016, queries/sqlc and repository are [locally verified](analysis/m03a1-verification.md);
atomic control-plane services and HTTP remain pending. No backfill or activation
was added. Apply the migration before new binaries; review preexisting registry
audit entity labels and coordinate any destructive downgrade. Do not
insert a second application's business data while current global legacy queries
remain reachable. Existing uniqueness on external_id/push_token is not the
future app/project-aware identity contract.

## Additive business ownership expansion — M03b.2a

Migration **000017** is [locally verified](analysis/m03b2a-verification.md).
[Exact schema and operator-tool contract](specs/m03b2-business-scope-expansion.md).
All eleven legacy business tables gain nullable application_id (no default),
validated app/composite parent references and immutable assigned ownership under
ordinary DML. Parent keys include app/id; deliveries additionally bind the device
and its user. Template deletion clears only template_id, retaining app scope.
Global external_id/push_token uniqueness remains conservative until scoped
identity/address policies exist. These are structural safeguards, not runtime
isolation: NULL legacy rows and global queries are still present.

Apply 000017 **before** the new binary; full-model projections now select the new
column. It takes table/index/constraint locks and needs a rehearsed maintenance
window. No implicit app/backfill is performed. The count-only preflight and
reviewed single-app SQL backfill require explicit operator mapping/quiescence;
see the exact contract before use. Down refuses any assigned business ownership
and cannot serve as a production rollback after mapping. New app business data,
final NOT NULL/scoped keys, scoped services/APIs and legacy retirement remain gated.

## Design Decisions

### Why is `notification_targets` stored separately?

Embedding recipient information directly inside the `notifications` table would require nullable columns such as `segment_id`, `user_id`, and `upload_batch_id`. This approach becomes difficult to maintain and extend.

Instead, the relationship is normalized, allowing a notification campaign to target any combination of users, segments, or uploaded recipients without modifying the schema.

### Why is `notification_deliveries` a separate table?

A notification campaign may target millions of users. Tracking delivery status inside the `notifications` table is not practical. Creating one delivery record per user enables individual delivery tracking, retry mechanisms, delivery analytics, and open rate calculations.

### Why are `device_tokens` separated from `users`?

A single user may own multiple devices (e.g., Android, iPhone, iPad). Each device has its own push token (OneSignal player ID). Separating device tokens ensures notifications can be delivered to every active device owned by the user.

### Why are `upload_batches` required?

Administrators can send notifications by uploading Excel files containing recipient information. The upload batch design provides full validation history and allows administrators to review failed records before sending notifications.

---

## Changelog

| Version | Date       | Description |
|---------|------------|-------------|
| 1.0     | 2026-07-23 | Initial database documentation |

### Ownership finalization at cutover

After reviewed backfill and writer retirement, the offline ownership finalizer
sets application_id NOT NULL on all eleven business tables in one statement.
See the [cutover procedure](specs/m03b-cutover.md) for verification and rollback
requirements.

### Modern installation bindings (M03c)

Migration 000018 adds mobile_installations and installation_bindings.
Installations keep a verifier, project, current owner, version and owner
epoch. Each immutable binding keeps its original owner, route and protected
address. A deferred composite foreign key matches active bindings to the
current installation owner and epoch. Retired bindings retain their original
attribution. Partial unique indexes enforce one active binding per
installation and one active address claim per provider namespace and type,
including across apps that share a provider namespace.

The application layer must supply the verifier, ciphertext, fingerprint and
trusted route. Migration rollback requires empty tables. See the
[installation and binding contract](specs/m03c-installation-binding.md).

### Durable campaign acceptance (M04)

Migration 000022 adds immutable acceptance timestamps/epochs to notifications,
a singleton deployment control row, explicit per-project dispatch policy,
actor-scoped send keys and the send outbox. Deployment starts sealed with a
migration hold; policies require explicit TTL values and coverage approval.
There are no production TTL defaults or public policy mutation endpoints.

The API commits the DRAFT claim, key snapshot, deadline, outbox and audit in
one transaction. UNIQUE(notification_id) permits one accepted generation while
resubmission is out of scope. Scheduling persists its original deadline and
creates the outbox only during atomic promotion. Expired schedules retain
FAILED/ttl_expired lifecycle evidence, without invented audience results.
Foreign keys retain campaign and staff attribution. Down refuses to remove
accepted/scheduled timestamps, keys or pending outbox evidence. See the
[M04 contract](specs/m04-durable-acceptance.md) and
[acceptance API](api/campaign-acceptance-api.md).

Due-schedule discovery joins the application public UUID and the singleton runtime,
selecting only unsealed exact origin/source lineage. It remains a nonlocking
metadata scan; per-campaign promotion rechecks under canonical transaction locks.
Expired schedules remain discoverable, and migration holds do not exclude already
accepted bookkeeping. This query-only change adds no schema/migration and avoids
stale/restored rows starving the first bounded polling page. See
[runtime composition](specs/m04-m05-runtime-composition.md).

### Atomic audience preparation (M05)

Migration 000023 separates modern notification_preparations and
notification_delivery_intents from legacy deliveries. One repeatable-read
transaction inserts the full binding-generation snapshot, immutable counts and
complete marker, then advances campaign state. Complete counts are immutable;
intent identity, original owner/epoch and counters cannot be rewritten or
decreased. Composite binding foreign keys preserve original attribution through
logout, token rotation and account transfer. The opaque delivery UUID begins at
version zero with an untouched attempt budget; it is not a receipt capability.

Holds remain on notifications with a deadline-bounded retry time. A hold or an
expired campaign has no fabricated complete snapshot. A true empty audience
does have a complete marker with known zero eligible counts. Down refuses
modern intent, snapshot or hold evidence. See the
[preparation specification](specs/m05-durable-preparation.md). Provider claims,
receipt capabilities, Kafka quarantine/offset handling and retention are later
slices; these tables alone do not implement them.

### Execution ownership and reservations (M06)

Migration 000028 adds campaign lease owner/generation/expiry and the latest
per-device attempt UUID/generation/timestamps. Generation and consumed attempt
count survive release/takeover. Owner/expiry and attempt metadata have explicit
NULL-pair constraints; latest attempt IDs are unique. A partial PROCESSING lease
index supports later bounded recovery; the existing PENDING due index remains
available for candidate discovery. No raw address or provider credential is copied.

The execution service checks current runtime/app/dispatch policy, exact business
lineage and complete preparation before leasing. Reservation additionally locks
and rechecks active user/current installation owner and immutable binding before
charging one due PENDING intent. A single sampled DB-clock value persists the
reservation timestamp and retry floor; lease/deadline predicates use fresh DB time.
There is no row-count/identity rewrite, budget refund or implicit whole-page claim.

The down migration takes table locks and refuses retained generation/attempt
history, even after release. It cannot erase fencing evidence on a used pipeline.
Empty up/down/up remains supported. Lease/reservation alone does not grant
provider permission. The separate [submission boundary](specs/m06-submission.md)
now consumes acknowledged reservations once and rechecks exact unresolved attempt,
current identity, frozen receipt metadata and live DB-clock lease/deadline after
credential/rate waits. Query-only changes add bounded payload reads and a remaining
lease/deadline interval; no schema change is required. Locks release before provider
I/O, and unknown validation commits authorize none. Attempt result persistence,
terminalization and update outbox remain open. See the [M06 contract](specs/m06-execution.md).

### Frozen receipt capability metadata (M06)

Migration 000029 adds one immutable notification_receipt_metadata row per delivery,
with an application/delivery FK backed by a scoped parent unique constraint.
Version, bounded ASCII key/environment labels and bounded Unix-second expiry are
non-null; no token, private key or registration address is stored. Existing intent,
preparation and installation identities provide the signed scope.

The first acknowledged device reservation freezes this row in the same transaction
as its attempt charge, after the delivery lock. A single accepted deadline plus an
explicit observation window determines expiry (Unix seconds rounded up). Retries
retain the row across lease/key/window changes. Missing metadata on an already
charged intent, foreign environment and insufficient expiry fail closed. Immutable
triggers prevent update/delete and down refuses populated metadata. Controlled
retention/erasure remains separate reviewed work. See the
[receipt/payload contract](specs/m06-receipt-payload.md#committed-signing-metadata).

### Source-aware send quarantine (M05)

Migration 000024 adds immutable physical log identities and bounded raw
quarantine evidence keyed by source/partition/offset. Sources start inactive;
topic name alone cannot identify an incarnation. Publication source_epoch stays
separate. Raw keys are BYTEA so malformed non-UTF-8 keys remain intact. Exact
redelivery is idempotent; conflicting bytes cannot overwrite existing evidence.
Reasons are allowlisted codes and no public endpoint exposes raw input.
Down refuses active sources or evidence. Source provisioning/verification,
restricted operator access, encryption at rest, replay and retention remain
production gates. See [quarantine contract](specs/m05-source-quarantine.md).

### Operator replay persistence (M05)

Migration 000025 adds three unwired replay tables. Request headers are immutable
and unique per application/request UUID; their 32-byte fingerprint binds the
complete normalized selection, actor/grant and change reference. An operational
grant UUID records provenance only: it is not an authorization check.

Attempts reference the existing physical quarantine position and optionally a
campaign through its application-scoped composite foreign key. Initial inserts
cannot claim publication or abandoned-cleanup success. Claim token, lease,
acceptance identity and finalized outcomes cannot be rewritten. One control row
per physical position serializes competing requests; it references its active
attempt or an immutable published/prepared/terminal resolution. Time-based lease
checks use database time rather than a time-dependent unique index.

The generated SQL exposes conditional claim, finish, expired-abandon and
resolution primitives. They require the replay service's authority, canonical
lock order and atomic audit transactions; SQLC calls alone do not establish that
protocol. Down refuses retained requests, attempts or control rows. No CLI,
service, operational grant verifier or publisher is enabled by this migration.
See the [reprocessing contract](specs/m05-operator-reprocessing.md).

### Revocable operational replay grants (M05)

Migration 000026 stores scoped operational grants separately from ordinary app
membership. Each grant binds one actor/application/request, trusted issuer,
deployment and complete selection fingerprint. Identity and expiry cannot be
rewritten; revocation is one-way. Retain grant evidence under the operational
retention policy; no deletion/issuance endpoint is provided. The down migration
refuses any retained grant. Request registration locks the grant through its
atomic header/audit transaction and evaluates expiry/freshness with database time.

A grant row alone is insufficient: the service also verifies the dedicated
step-up signature and current DB send authority/explicit app membership. This
remains an unwired library; per-position replay and real issuer integration are
open. See [registration verification](analysis/m05-replay-registration-verification.md).

### Replay claim dispositions (M05)

Migration 000027 adds `already_resolved` to immutable attempt outcomes. A new
request can record that prior evidence resolution already exists without claiming
another broker publication. Down refuses retained rows using this disposition.
Claim/control/attempt audit commit together; an uncertain commit returns no new
execution proof. [Claim verification](analysis/m05-replay-claim-verification.md)
describes the claim boundary. [Publication/recovery verification](analysis/m05-replay-execution-verification.md)
now covers the unwired execution libraries: outcome/control/audit commit together,
expired cleanup releases only the exact token, and no evidence is deleted.
Runtime/CLI/source lifecycle gates remain open.
