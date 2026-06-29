# Notification Backend Database Documentation

# Overview

This backend service is a **Notification Management System** that enables administrators to send push notifications to mobile applications through **Firebase Cloud Messaging (FCM)**.

The system follows an event-driven architecture where notification requests are persisted in the database, published to Kafka, and processed asynchronously by a notification worker.

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
```

The database stores and manages the following entities:

* Mobile users
* Device tokens
* Administrator accounts
* Notification campaigns
* Notification templates
* User segments
* Notification delivery history
* Notification read status
* Audit logs

---

# Entity Relationship

```text
Admin Users
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

# Database Tables

## users

### Purpose

Stores all mobile application users.

Notifications are **never sent directly to device tokens**. Instead, they are always targeted at users, and the system resolves all registered devices belonging to those users.

```text
User
    │
    ▼
Device Tokens
```

This design allows a single user to own multiple devices.

### Columns

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

## device_tokens

### Purpose

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

---

## roles

### Purpose

Defines administrator roles within the system.

Example roles:

```text
SUPER_ADMIN

ADMIN

OPERATOR
```

---

## admin_users

### Purpose

Stores administrator accounts that can access the Web Admin portal.

Example:

```text
admin@company.com
```

This table is completely independent from the `users` table, since administrators are not mobile application users.

---

## templates

### Purpose

Stores reusable notification templates.

Example:

**Title**

```text
Hello {{name}}
```

**Body**

```text
Your order {{order_number}} has been shipped.
```

Before sending notifications, the worker replaces placeholders with actual user-specific values.

---

## segments

### Purpose

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

---

## segment_members

### Purpose

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

---

## notifications

### Purpose

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

### Campaign Lifecycle

```text
DRAFT
    │
    ▼
SCHEDULED
    │
    ▼
PROCESSING
    │
    ▼
SENT
```

---

## notification_targets

### Purpose

Defines the recipients of a notification campaign.

Instead of storing recipient information directly inside the `notifications` table, all targets are stored separately to support multiple targeting strategies.

Supported target types include:

```text
ALL

SEGMENT

USER

UPLOAD
```

Example:

```text
Notification A
    │
    ├── Jakarta Segment
    ├── Bandung Segment
    ├── User #25
    └── Upload Batch #18
```

A single notification campaign may have multiple targets.

---

## upload_batches

### Purpose

Stores metadata for Excel uploads used in notification targeting.

Example:

```text
Employee.xlsx

1,000 Rows

980 Valid

20 Invalid
```

---

## upload_batch_rows

### Purpose

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

---

## notification_deliveries

### Purpose

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

### Delivery Status Flow

```text
PENDING
    │
    ▼
SENT
    │
    ▼
DELIVERED
    │
    ▼
OPENED
```

If delivery fails:

```text
FAILED
```

Example:

| Notification | User   | Status    |
| ------------ | ------ | --------- |
| Promo        | User A | DELIVERED |
| Promo        | User B | FAILED    |
| Promo        | User C | OPENED    |

---

## notification_reads

### Purpose

Records when users open notifications.

This information is used for analytics such as:

* Read Rate
* Open Rate
* User Engagement

A successfully delivered notification does not necessarily mean it has been opened by the recipient.

---

## audit_logs

### Purpose

Records all administrative actions performed through the Web Admin portal.

Examples:

```text
Create Notification

Delete Segment

Edit Template
```

Sample records:

| Actor   | Action              |
| ------- | ------------------- |
| Admin A | CREATE_NOTIFICATION |
| Admin B | DELETE_TEMPLATE     |
| Admin A | IMPORT_USERS        |

---

# Notification Lifecycle

```text
Admin
    │
    ▼
Create Notification
    │
    ▼
notifications
    │
    ▼
notification_targets
    │
    ▼
Publish to Kafka
    │
    ▼
Notification Worker
    │
    ▼
device_tokens
    │
    ▼
Firebase Cloud Messaging
    │
    ▼
notification_deliveries
    │
    ▼
Mobile Application
    │
    ▼
notification_reads
```

---

# Design Decisions

## Why is `notification_targets` stored separately?

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

## Why is `notification_deliveries` a separate table?

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

* Individual delivery tracking
* Retry mechanisms
* Delivery analytics
* Open rate calculations

---

## Why are `device_tokens` separated from `users`?

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

## Why are `upload_batches` required?

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

# Future Scalability

The current schema is designed to support future enhancements with minimal structural changes.

Supported features include:

* ✅ Scheduled notifications
* ✅ Kafka-based asynchronous processing
* ✅ Firebase Cloud Messaging (FCM)
* ✅ Retry mechanisms
* ✅ Delivery analytics
* ✅ Open rate analytics
* ✅ Multiple devices per user
* ✅ Segment-based notifications
* ✅ Excel import
* ✅ Notification templates
* ✅ Audit logging
* ✅ Multi-admin support
* ✅ Role-based access control

---

# Future Enhancements

The following improvements can be implemented as the system grows:

### Retry Queue & Dead Letter Queue (DLQ)

Messages that repeatedly fail delivery can be redirected to a Dead Letter Queue (DLQ) for further investigation and reprocessing.

---

### Multi-provider Push Notifications

Support additional push notification providers (such as Huawei Push Kit or direct APNs integration) by introducing a notification provider abstraction.

---

### Campaign Analytics

Introduce aggregated statistics tables to store metrics such as:

* Total targeted users
* Successfully delivered notifications
* Failed deliveries
* Open rate
* Click-through rate (CTR)

This avoids expensive real-time aggregation from `notification_deliveries`.

---

### In-App Notifications

If the mobile application requires an in-app notification center, the existing `notifications` and `notification_reads` tables can be extended without requiring major schema changes.

The current architecture has been intentionally designed to accommodate this feature in the future.
