# Notification Backend Database Documentation

## Overview

This backend service is a **Notification Management System** that enables administrators to send push notifications to mobile applications through **Firebase Cloud Messaging (FCM)**.

The system follows an event-driven architecture where notification requests are persisted in the database, published to Kafka, and processed asynchronously by a notification worker.

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
Firebase Cloud Messaging (FCM)
    │
    ▼
Mobile Application

The database stores and manages the following entities:

- Mobile users
- Device tokens
- Staff / administrator accounts
- Notification campaigns
- Notification templates
- User segments
- Notification delivery history
- Notification read status
- Audit logs

---

## Entity Relationship

```text
Staff Users
   │
   ├──────────────┐
   ▼              ▼
Templates     Notifications
                   │
                   ▼
         Notification Targets
                   │
         ┌─────────┴─────────┐
         ▼                   ▼
      Segments         Upload Batches
         │
         ▼
 Segment Members
         │
         ▼
        Users
         │
         ▼
   Device Tokens
         │
         ▼
Notification Deliveries
         │
         ▼
 Notification Reads
```

---

## Database Tables

### users

**Purpose**

Stores all mobile application users.

Notifications are **never sent directly to device tokens**. Instead, they are always targeted at users, and the system resolves all registered devices belonging to those users.

```text
User
    │
    ▼
Device Tokens
```

This design allows a single user to own multiple devices.

**Columns**

| Column      | Description                                          |
| ----------- | ---------------------------------------------------- |
| id          | Primary key                                          |
| external_id | Identifier from an external system (HRIS, ERP, etc.) |
| name        | User's full name                                     |
| email       | User email address                                   |
| status      | ACTIVE or INACTIVE                                   |
| created_at  | Record creation timestamp                            |
| updated_at  | Last update timestamp                                |

---

### device_tokens

**Purpose**

Stores Firebase Cloud Messaging (FCM) registration tokens.

Each user may sign in from multiple devices, each having its own FCM token.

Example:

```text
User
├── Android
├── iPhone
└── Tablet
```

When processing notifications, the Notification Worker retrieves all active device tokens associated with the target users.

**Columns**

| Column          | Description                                    |
| --------------- | ---------------------------------------------- |
| id              | Primary key                                    |
| user_id         | FK to users.id                                 |
| provider        | FCM, APNS, or HUAWEI                           |
| platform        | ANDROID, IOS, or WEB                           |
| installation_id | Optional installation identifier               |
| push_token      | FCM registration token (unique)                |
| app_version     | Application version                            |
| os_version      | Operating system version                       |
| device_model    | Device model name                              |
| is_active       | Whether the token is still valid               |
| last_seen_at    | Last time the token was used                   |
| created_at      | Record creation timestamp                      |
| updated_at      | Last update timestamp                          |

---

### roles

**Purpose**

Defines administrator roles within the system.

Example roles:

```text
SUPER_ADMIN
ADMIN
OPERATOR
```

**Columns**

| Column      | Description                  |
| ----------- | ---------------------------- |
| id          | Primary key                  |
| name        | Role name (unique)           |
| description | Optional role description    |
| created_at  | Record creation timestamp    |
| updated_at  | Last update timestamp        |

---

### staff_users

**Purpose**

Stores staff / administrator accounts that can access the Web Admin portal.

This table is completely independent from the `users` table, since staff members are not mobile application users.

**Columns**

| Column        | Description                                    |
| ------------- | ---------------------------------------------- |
| id            | Primary key                                    |
| role_id       | FK to roles.id                                 |
| name          | Staff full name                                |
| email         | Staff email address (unique)                   |
| password_hash | Bcrypt hashed password                         |
| is_active     | Whether the account is active                  |
| created_at    | Record creation timestamp                      |
| updated_at    | Last update timestamp                          |

---

### templates

**Purpose**

Stores reusable notification templates.

Example:

**Title Template**

```text
Hello {{name}}
```

**Body Template**

```text
Your order {{order_number}} has been shipped.
```

Before sending notifications, the worker replaces placeholders with actual user-specific values.

**Columns**

| Column          | Description                                    |
| --------------- | ---------------------------------------------- |
| id              | Primary key                                    |
| name            | Template name                                  |
| title_template  | Title template with placeholders               |
| body_template   | Body template with placeholders                |
| variables       | JSONB array describing available variables     |
| created_by      | FK to staff_users.id                           |
| is_active       | Whether the template is enabled                |
| created_at      | Record creation timestamp                      |
| updated_at      | Last update timestamp                          |

---

### segments

**Purpose**

Represents logical groups of users that can receive notifications collectively.

Examples:

```text
Jakarta
Premium Members
Gold Members
Powerlifting Club
Marketing Team
```

A notification campaign can target one or more user segments.

**Columns**

| Column      | Description                         |
| ----------- | ----------------------------------- |
| id          | Primary key                         |
| name        | Segment name                        |
| description | Optional segment description        |
| created_by  | FK to staff_users.id                |
| created_at  | Record creation timestamp           |
| updated_at  | Last update timestamp               |

---

### segment_members

**Purpose**

Defines the relationship between segments and users.

```text
Segment
    │
    ▼
Users
```

Example:

```text
Segment: Jakarta
├── User 1
├── User 2
└── User 3
```

**Columns**

| Column      | Description                     |
| ----------- | ------------------------------- |
| id          | Primary key                     |
| segment_id  | FK to segments.id               |
| user_id     | FK to users.id                  |
| created_at  | Record creation timestamp       |

The combination `(segment_id, user_id)` is unique.

---

### notifications

**Purpose**

Represents a notification campaign.

Each record corresponds to one notification campaign.

Example:

**Title**

```text
Weekend Promotion
```

**Body**

```text
50% Discount on All Items
```

**Columns**

| Column        | Description                                    |
| ------------- | ---------------------------------------------- |
| id            | Primary key                                    |
| template_id   | FK to templates.id (nullable)                  |
| template_name | Snapshot of template name at creation time     |
| title         | Notification title (may be rendered)           |
| body          | Notification body (may be rendered)            |
| payload       | JSONB additional metadata for the app          |
| priority      | NORMAL or HIGH                                 |
| status        | DRAFT, SCHEDULED, QUEUED, PROCESSING, COMPLETED, FAILED, or CANCELLED |
| scheduled_at  | When the notification should be sent           |
| published_at  | When it was actually sent                      |
| completed_at  | When delivery processing finished              |
| created_by    | FK to staff_users.id                           |
| created_at    | Record creation timestamp                      |
| updated_at    | Last update timestamp                          |

**Campaign Lifecycle**

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

- `DRAFT` – editable; not yet sent.
- `SCHEDULED` – waiting for scheduled time.
- `QUEUED` – published to Kafka, waiting for worker.
- `PROCESSING` – worker is currently processing.
- `COMPLETED` – successfully sent to all targets.
- `FAILED` – failed permanently.
- `CANCELLED` – cancelled by admin.

---

### notification_targets

**Purpose**

Defines the recipients of a notification campaign.

Instead of storing recipient information directly inside the `notifications` table, all targets are stored separately to support multiple targeting strategies.

Supported target types:

```text
GLOBAL    → all active users
SEGMENT   → users belonging to a specific segment
USER      → specific individual users
UPLOAD    → users imported from an Excel file
```

Example:

```text
Notification A
    │
    ├── GLOBAL
    ├── SEGMENT (Jakarta)
    ├── SEGMENT (Bandung)
    ├── USER (User #25)
    └── UPLOAD (Batch #18)
```

A single notification campaign may have multiple targets.

**Columns**

| Column           | Description                                    |
| ---------------- | ---------------------------------------------- |
| id               | Primary key                                    |
| notification_id  | FK to notifications.id                         |
| target_type      | GLOBAL, SEGMENT, USER, or UPLOAD               |
| segment_id       | FK to segments.id (if target_type = SEGMENT)   |
| user_id          | FK to users.id (if target_type = USER)         |
| upload_batch_id  | FK to upload_batches.id (if target_type = UPLOAD) |
| created_at       | Record creation timestamp                      |

The `CHECK` constraint ensures exactly one of `segment_id`, `user_id`, or `upload_batch_id` is set according to `target_type`.

---

### upload_batches

**Purpose**

Stores metadata for Excel uploads used in notification targeting.

Example:

```text
Employee.xlsx
1,000 Rows
980 Valid
20 Invalid
```

**Columns**

| Column            | Description                                    |
| ----------------- | ---------------------------------------------- |
| id                | Primary key                                    |
| uploaded_by       | FK to staff_users.id                           |
| original_filename | Original filename of the uploaded file         |
| total_rows        | Total rows in the file                         |
| valid_rows        | Rows that passed validation                    |
| invalid_rows      | Rows that failed validation                    |
| created_at        | Record creation timestamp                      |

---

### upload_batch_rows

**Purpose**

Stores validation results for every row within an uploaded Excel file.

Example:

```text
Row 1
External ID: EMP001
Status: VALID
```

```text
Row 2
External ID: EMP999
Status: INVALID
Reason: User not found
```

This allows administrators to review which rows failed validation and why.

**Columns**

| Column        | Description                                    |
| ------------- | ---------------------------------------------- |
| id            | Primary key                                    |
| batch_id      | FK to upload_batches.id                        |
| external_id   | Identifier from the uploaded file              |
| is_valid      | True if validation passed                      |
| error_message | Reason for failure (if any)                    |
| created_at    | Record creation timestamp                      |

---

### notification_deliveries

**Purpose**

Tracks the delivery status of notifications for every targeted user.

After consuming a Kafka event, the Notification Worker creates one delivery record per user.

Example:

```text
Notification
    │
    ▼
100 Users
    │
    ▼
100 Delivery Records
```

**Delivery Status Flow**

```text
PENDING
    │
    ▼
SENT (sent to FCM)
    │
    ├──────────┐
    ▼          ▼
DELIVERED   FAILED
    │
    ▼
OPENED
```

- `PENDING` – delivery record created, not yet attempted.
- `SENT` – sent to FCM, awaiting delivery confirmation.
- `DELIVERED` – delivered to the device.
- `OPENED` – user opened the notification.
- `FAILED` – delivery failed permanently.

**Columns**

| Column              | Description                                    |
| ------------------- | ---------------------------------------------- |
| id                  | Primary key                                    |
| notification_id     | FK to notifications.id                         |
| user_id             | FK to users.id                                 |
| device_token_id     | FK to device_tokens.id                         |
| provider            | FCM, APNS, or HUAWEI                           |
| provider_message_id | Identifier returned by the provider            |
| status              | PENDING, SENT, DELIVERED, OPENED, or FAILED    |
| retry_count         | Number of retry attempts                       |
| failed_reason       | Reason if status = FAILED                      |
| sent_at             | When the message was sent to the provider      |
| delivered_at        | When the device confirmed delivery             |
| opened_at           | When the user opened the notification          |
| created_at          | Record creation timestamp                      |
| updated_at          | Last update timestamp                          |

---

### notification_reads

**Purpose**

Records when users open notifications.

This information is used for analytics such as:

- Read Rate
- Open Rate
- User Engagement

A successfully delivered notification does not necessarily mean it has been opened by the recipient.

**Columns**

| Column          | Description                                    |
| --------------- | ---------------------------------------------- |
| id              | Primary key                                    |
| notification_id | FK to notifications.id                         |
| user_id         | FK to users.id                                 |
| read_at         | When the user opened the notification          |

The combination `(notification_id, user_id)` is unique.

---

### audit_logs

**Purpose**

Records all administrative actions performed through the Web Admin portal.

Examples:

```text
Create Notification
Delete Segment
Edit Template
```

**Columns**

| Column        | Description                                    |
| ------------- | ---------------------------------------------- |
| id            | Primary key                                    |
| actor_user_id | FK to staff_users.id                           |
| action        | Action performed (e.g., CREATE_NOTIFICATION)   |
| entity_type   | Type of affected entity (e.g., "notification") |
| entity_name   | Human-readable entity name                     |
| entity_id     | ID of the affected entity                      |
| before_json   | JSON snapshot before the change                |
| after_json    | JSON snapshot after the change                 |
| ip_address    | IP address of the actor                        |
| user_agent    | User agent string                              |
| created_at    | Record creation timestamp                      |

---

## Notification Lifecycle

```text
Admin
    │
    ▼
Create Notification (DRAFT)
    │
    ▼
Admin clicks "Send"
    │
    ▼
Notification status becomes QUEUED
    │
    ▼
Publish to Kafka
    │
    ▼
Notification Worker
    │
    ▼
Worker expands targets → user list
    │
    ▼
Fetch device_tokens for each user
    │
    ▼
Send to Firebase Cloud Messaging
    │
    ▼
Insert notification_deliveries records
    │
    ▼
Update notification status → COMPLETED or FAILED
    │
    ▼
Mobile Application receives notification
    │
    ▼
App reports opened → notification_reads
```

---

## Design Decisions

### Why is `notification_targets` stored separately?

Embedding recipient information directly inside the `notifications` table would require nullable columns such as:

```text
segment_id
user_id
upload_batch_id
```

This approach becomes difficult to maintain and extend.

Instead, the relationship is normalized:

```text
Notification
    │
    ▼
Target
    │
    ▼
Target
    │
    ▼
Target
```

This design allows a notification campaign to target any combination of users, segments, or uploaded recipients without modifying the schema.

---

### Why is `notification_deliveries` a separate table?

A notification campaign may target millions of users.

Example:

```text
Notification
    │
    ▼
1,000,000 Users
```

Tracking delivery status inside the `notifications` table is not practical.

Creating one delivery record per user enables:

- Individual delivery tracking
- Retry mechanisms
- Delivery analytics
- Open rate calculations

---

### Why are `device_tokens` separated from `users`?

A single user may own multiple devices.

```text
Greg
    │
    ├── Android
    ├── iPhone
    └── iPad
```

Each device has its own FCM registration token.

Separating device tokens ensures notifications can be delivered to every active device owned by the user.

---

### Why are `upload_batches` required?

Administrators can send notifications by uploading Excel files containing recipient information.

Processing flow:

```text
Upload Excel
    │
    ▼
Validation
    │
    ▼
upload_batches
    │
    ▼
upload_batch_rows
    │
    ▼
notification_targets
    │
    ▼
Kafka
    │
    ▼
Notification Worker
```

This design provides full validation history and allows administrators to review failed records before sending notifications.

---

## Future Scalability

The current schema is designed to support future enhancements with minimal structural changes.

Supported features include:

- ✅ Scheduled notifications
- ✅ Kafka-based asynchronous processing
- ✅ Firebase Cloud Messaging (FCM)
- ✅ Retry mechanisms
- ✅ Delivery analytics
- ✅ Open rate analytics
- ✅ Multiple devices per user
- ✅ Segment-based notifications
- ✅ Excel import
- ✅ Notification templates
- ✅ Audit logging
- ✅ Multi-admin support
- ✅ Role-based access control

---

## Future Enhancements

The following improvements can be implemented as the system grows:

### Retry Queue & Dead Letter Queue (DLQ)

Messages that repeatedly fail delivery can be redirected to a Dead Letter Queue (DLQ) for further investigation and reprocessing.

---

### Multi-provider Push Notifications

Support additional push notification providers (such as Huawei Push Kit or direct APNs integration) by introducing a notification provider abstraction.

---

### Campaign Analytics

Introduce aggregated statistics tables to store metrics such as:

- Total targeted users
- Successfully delivered notifications
- Failed deliveries
- Open rate
- Click-through rate (CTR)

This avoids expensive real-time aggregation from `notification_deliveries`.

---

### In-App Notifications

If the mobile application requires an in-app notification center, the existing `notifications` and `notification_reads` tables can be extended without requiring major schema changes.

The current architecture has been intentionally designed to accommodate this feature in the future.
