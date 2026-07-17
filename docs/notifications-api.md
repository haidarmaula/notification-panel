# Notifications API Documentation

**Version:** 1.0  
**Base URL:** `/api/v1`  
**Content-Type:** `application/json`

---

## Authentication

All endpoints require authentication via:
- **API Key**: Passed in the `X-API-Key` header.
- **JWT Token**: Passed in the `Authorization: Bearer <token>` header.

The authenticated staff ID is extracted from the JWT and used as the `created_by` field when creating notifications.

---

## Common Error Responses

| Status Code | Message | Description |
|-------------|---------|-------------|
| `400` | Invalid request body | Malformed JSON or validation error |
| `401` | Unauthorized | Missing or invalid API Key / JWT |
| `404` | Resource not found | The requested resource does not exist |
| `409` | Conflict | Resource state conflict (e.g., cannot update non-draft notification) |
| `500` | Internal server error | Unexpected server error |

---

## Notification Object

| Field | Type | Description |
|-------|------|-------------|
| `id` | integer | Notification ID |
| `title` | string | Notification title |
| `body` | string | Notification body content |
| `template` | object (optional) | Template used (`id`, `name`) |
| `type` | string | `BROADCAST`, `SEGMENT`, or `INDIVIDUAL` |
| `status` | string | `DRAFT`, `SCHEDULED`, `QUEUED`, `PROCESSING`, `COMPLETED`, `FAILED`, `CANCELLED` |
| `created_by` | object | Staff user who created it (`id`, `name`) |
| `scheduled_at` | timestamp (nullable) | Scheduled send time |
| `sent_at` | timestamp (nullable) | Actual send time |
| `published_at` | timestamp (nullable) | Published time |
| `completed_at` | timestamp (nullable) | Processing completion time |
| `created_at` | timestamp | Creation timestamp |
| `updated_at` | timestamp | Last update timestamp |
| `statistics` | object | Delivery statistics (`targeted`, `delivered`, `opened`) |

---

## 1. Notifications

### 1.1 List Notifications
**`GET /notifications`**

Retrieves a paginated list of notifications with optional filters.

#### Query Parameters
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `page` | integer | 1 | Page number |
| `limit` | integer | 10 | Items per page (max 100) |
| `status` | string | - | Filter by status (`DRAFT`, `SCHEDULED`, `QUEUED`, `PROCESSING`, `COMPLETED`, `FAILED`, `CANCELLED`) |
| `type` | string | - | Filter by target type (`BROADCAST`, `SEGMENT`, `INDIVIDUAL`) |
| `keyword` | string | - | Search by title (case-insensitive, partial match) |

#### Success Response (200 OK)
```json
{
  "data": [
    {
      "id": 15,
      "title": "Promo Juli",
      "type": "SEGMENT",
      "status": "COMPLETED",
      "created_by": "Greg",
      "scheduled_at": "2025-07-01T10:00:00Z",
      "sent_at": "2025-07-01T10:05:00Z",
      "created_at": "2025-06-30T08:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 143
  }
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `500` | Internal server error | Database query failed |

---

### 1.2 Get Notification by ID
**`GET /notifications/{id}`**

Retrieves full notification details including delivery statistics.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Notification ID |

#### Success Response (200 OK)
```json
{
  "id": 15,
  "title": "Promo Juli",
  "body": "Diskon sampai 50%",
  "template": {
    "id": 3,
    "name": "Promo Template"
  },
  "type": "SEGMENT",
  "status": "COMPLETED",
  "created_by": {
    "id": 1,
    "name": "Greg"
  },
  "scheduled_at": "2025-07-01T10:00:00Z",
  "sent_at": "2025-07-01T10:05:00Z",
  "published_at": "2025-07-01T10:05:00Z",
  "completed_at": "2025-07-01T10:06:00Z",
  "created_at": "2025-06-30T08:00:00Z",
  "updated_at": "2025-07-01T10:06:00Z",
  "statistics": {
    "targeted": 1200,
    "delivered": 1180,
    "opened": 960
  }
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid id | Invalid ID format |
| `404` | notification not found | Notification does not exist |
| `500` | Internal server error | Database query failed |

---

### 1.3 Create Notification
**`POST /notifications`**

Creates a new notification draft. The notification is always created with status `DRAFT` (or `SCHEDULED` if `scheduled_at` is provided).

#### Request Body
```json
{
  "title": "Promo Spesial",
  "body": "Diskon 50% untuk semua produk!",
  "template_id": 3,
  "type": "BROADCAST",
  "segment_id": 5,
  "user_ids": [10, 20, 30],
  "scheduled_at": "2025-07-05T10:00:00Z"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `title` | string | ✅ | Notification title |
| `body` | string | ✅ | Notification body content |
| `template_id` | integer | ❌ | Template ID (must exist if provided) |
| `type` | string | ✅ | `BROADCAST`, `SEGMENT`, or `INDIVIDUAL` |
| `segment_id` | integer | ⚠️ | Required if `type = SEGMENT` |
| `user_ids` | array | ⚠️ | Required if `type = INDIVIDUAL` (min 1) |
| `scheduled_at` | timestamp | ❌ | Future timestamp; if provided, status becomes `SCHEDULED` |


#### Success Response (201 Created)
```json
{
  "id": 15,
  "status": "DRAFT"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | title and body required | Missing required fields |
| `400` | invalid target type | Invalid `type` value |
| `400` | template not found | `template_id` does not exist |
| `400` | segment not found | `segment_id` does not exist (for `SEGMENT`) |
| `400` | user_ids required for INDIVIDUAL type | Missing `user_ids` for `INDIVIDUAL` |
| `400` | segment_id required for SEGMENT type | Missing `segment_id` for `SEGMENT` |
| `400` | scheduled time must be in the future | `scheduled_at` is in the past |
| `401` | unauthorized | Invalid or missing authentication |
| `500` | Internal server error | Database insert failed |

---

### 1.4 Update Notification
**`PATCH /notifications/{id}`**

Updates a draft notification. **Allowed only if status is `DRAFT`**.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Notification ID |

#### Request Body (all fields optional)
```json
{
  "title": "Promo Baru",
  "body": "Diskon 60%",
  "template_id": 4,
  "scheduled_at": "2025-07-06T10:00:00Z"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `title` | string | ❌ | New title |
| `body` | string | ❌ | New body content |
| `template_id` | integer | ❌ | New template ID (must exist) |
| `scheduled_at` | timestamp | ❌ | New scheduled time (must be future) |

#### Success Response (200 OK)
```json
{
  "message": "notification updated"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid id | Invalid ID format |
| `400` | at least one field required | No fields provided for update |
| `400` | invalid request body | Malformed JSON |
| `404` | notification not found | Notification does not exist |
| `409` | notification must be in DRAFT status | Notification is not in `DRAFT` |
| `500` | Internal server error | Database update failed |

---

### 1.5 Delete Notification
**`DELETE /notifications/{id}`**

Deletes a draft notification and its targets. **Allowed only if status is `DRAFT`**.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Notification ID |

#### Success Response (200 OK)
```json
{
  "message": "deleted"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid id | Invalid ID format |
| `404` | notification not found | Notification does not exist |
| `409` | cannot delete sent notification | Notification is not in `DRAFT` |
| `500` | Internal server error | Database delete failed |

---

### 1.6 Send Notification
**`POST /notifications/{id}/send`**

Queues a draft notification for sending by publishing a `notification.send.requested` event to Kafka. The notification status is updated to `QUEUED`, and a worker processes it asynchronously.

**Allowed only if status is `DRAFT`.**

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Notification ID |

#### Request Body
None

#### Success Response (202 Accepted)
```json
{
  "message": "notification queued for sending"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid notification id | Invalid ID format |
| `400` | notification must be in DRAFT status | Notification is not in `DRAFT` |
| `404` | notification not found | Notification does not exist |
| `500` | failed to queue notification | Kafka producer error |
| `500` | Internal server error | Unexpected error |

---

## Notification Lifecycle

```text
DRAFT
    │
    ├── (if scheduled_at provided)
    │
    ▼
SCHEDULED
    │
    ▼ (after calling POST /send)
    │
    ▼
QUEUED (published to Kafka)
    │
    ▼ (worker picks up)
    │
    ▼
PROCESSING
    │
    ├──────────┐
    ▼          ▼
COMPLETED   FAILED
```

| Status | Description |
|--------|-------------|
| `DRAFT` | Editable; not yet sent |
| `SCHEDULED` | Waiting for scheduled time |
| `QUEUED` | Published to Kafka, waiting for worker |
| `PROCESSING` | Worker is processing delivery |
| `COMPLETED` | Successfully sent to all targets |
| `FAILED` | Processing failed (permanent) |
| `CANCELLED` | Cancelled by admin (future) |

---

## Delivery Statistics

The `statistics` object in the detail response shows:

| Field | Description |
|-------|-------------|
| `targeted` | Total users targeted by the notification |
| `delivered` | Number of notifications delivered to devices |
| `opened` | Number of notifications opened by users |

**Note:** For `BROADCAST` notifications, `targeted` counts all active users. For `SEGMENT` or `INDIVIDUAL`, it counts the specific users targeted.

---

## Changelog

| Version | Date | Description |
|---------|------|-------------|
| 1.0 | 2025-07-17 | Initial API documentation for Notifications feature |
