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

### Example Actions

| Action                    | Description                                    |
| ------------------------- | ---------------------------------------------- |
| `STAFF_CREATE`            | A new staff user was created                   |
| `STAFF_UPDATE`            | A staff user was updated                       |
| `STAFF_DELETE`            | A staff user was deleted                       |
| `SEGMENT_CREATE`          | A new segment was created                      |
| `NOTIFICATION_SEND`       | A notification was sent                        |

---

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
