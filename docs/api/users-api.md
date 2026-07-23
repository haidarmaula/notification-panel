# Users & Device Tokens API Documentation

**Version:** 1.0
**Base URL:** `/api/v1`
**Content-Type:** `application/json`

---

## Authentication

All endpoints require authentication via:
- **API Key**: Passed in the `X-API-Key` header.
- **JWT Token**: Passed in the `Authorization: Bearer <token>` header.

The authenticated staff ID is extracted from the JWT and used for authorization checks.

**Note:** Device token endpoints (`/device-tokens`) are designed for mobile app usage. For production, authentication should use a mobile JWT or API key. Currently, `user_id` is passed in the request body for simplicity.

---

## Common Error Responses

| Status Code | Message | Description |
|-------------|---------|-------------|
| `400` | Invalid request body | Malformed JSON or validation error |
| `401` | Unauthorized | Missing or invalid API Key / JWT |
| `404` | Resource not found | The requested resource does not exist |
| `409` | Conflict | Duplicate resource or state conflict |
| `500` | Internal server error | Unexpected server error |

---

## 1. Users

### 1.1 List Users
**`GET /users`**

Retrieves a paginated list of users with optional filters.

#### Query Parameters
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `page` | integer | 1 | Page number |
| `limit` | integer | 10 | Items per page (max 100) |
| `keyword` | string | - | Search by name, email, or external_id (case-insensitive) |
| `status` | string | - | Filter by user status (`ACTIVE` or `INACTIVE`) |
| `external_id` | string | - | Exact match by external_id (fast path) |

#### Success Response (200 OK)
```json
{
  "data": [
    {
      "id": 1,
      "external_id": "USR001",
      "name": "John Doe",
      "email": "john@example.com",
      "status": "ACTIVE",
      "created_at": "2025-07-14T10:00:00Z",
      "updated_at": "2025-07-14T10:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 5300
  }
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `500` | Internal server error | Database query failed |

---

### 1.2 Search Users (Autocomplete)
**`GET /users/search`**

Provides a lightweight user search for autocomplete/typeahead functionality. Used by admin interfaces when adding members to segments, personal notifications, or Excel validation.

#### Query Parameters
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `keyword` | string | ✅ | Search term (minimum 1 character) |

#### Success Response (200 OK)
```json
[
  {
    "id": 1,
    "external_id": "USR001",
    "name": "John Doe"
  },
  {
    "id": 25,
    "external_id": "USR025",
    "name": "Johnny"
  }
]
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | keyword is required | `keyword` parameter is missing or empty |
| `500` | Internal server error | Database query failed |

---

### 1.3 Get User by ID
**`GET /users/{id}`**

Retrieves detailed information about a specific user.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | User ID |

#### Success Response (200 OK)
```json
{
  "id": 1,
  "external_id": "USR001",
  "name": "John Doe",
  "email": "john@example.com",
  "status": "ACTIVE",
  "created_at": "2025-07-14T10:00:00Z",
  "updated_at": "2025-07-14T10:00:00Z"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid user id | Invalid ID format |
| `404` | user not found | User does not exist |
| `500` | Internal server error | Database query failed |

---

### 1.4 Get User Device Tokens
**`GET /users/{id}/device-tokens`**

Retrieves all device tokens registered for a specific user. Used by admin to view devices owned by a user.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | User ID |

#### Success Response (200 OK)
```json
{
  "data": [
    {
      "id": 12,
      "platform": "ANDROID",
      "installation_id": "inst_abc123",
      "is_active": true,
      "last_seen_at": "2025-07-14T09:00:00Z",
      "created_at": "2025-07-10T08:00:00Z"
    },
    {
      "id": 15,
      "platform": "IOS",
      "installation_id": "inst_xyz789",
      "is_active": true,
      "last_seen_at": "2025-07-13T22:00:00Z",
      "created_at": "2025-07-11T14:30:00Z"
    }
  ]
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid user id | Invalid ID format |
| `404` | user not found | User does not exist |
| `500` | Internal server error | Database query failed |

---

### 1.5 Get User Segments
**`GET /users/{id}/segments`**

Retrieves all segments that a user belongs to. Useful for understanding user categorization and segment membership.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | User ID |

#### Success Response (200 OK)
```json
{
  "data": [
    {
      "id": 2,
      "name": "Marketing"
    },
    {
      "id": 5,
      "name": "Premium Users"
    }
  ]
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid user id | Invalid ID format |
| `404` | user not found | User does not exist |
| `500` | Internal server error | Database query failed |

---

### 1.6 Get User Notification History
**`GET /users/{id}/notifications`**

Retrieves the notification delivery history for a specific user. Shows all notifications that were sent to the user and their current status.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | User ID |

#### Query Parameters
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `page` | integer | 1 | Page number |
| `limit` | integer | 10 | Items per page (max 100) |

#### Success Response (200 OK)
```json
{
  "data": [
    {
      "notification_id": 15,
      "title": "Promo Juli",
      "status": "OPENED",
      "opened_at": "2025-07-14T10:30:00Z"
    },
    {
      "notification_id": 12,
      "title": "Welcome Message",
      "status": "DELIVERED",
      "opened_at": null
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 10,
    "total": 42
  }
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid user id | Invalid ID format |
| `404` | user not found | User does not exist |
| `500` | Internal server error | Database query failed |

---

## 2. Device Tokens (Mobile API)

These endpoints are primarily used by mobile applications to register, update, and delete FCM/APNS push tokens.

---

### 2.1 Register Device Token
**`POST /device-tokens`**

Registers a new device token for a user. Each token is unique per device. If the token already exists, returns a conflict error.

#### Request Body
```json
{
  "user_id": 1,
  "platform": "ANDROID",
  "push_token": "fcm_token_abc123...",
  "installation_id": "inst_xyz789",
  "app_version": "2.5.0",
  "os_version": "Android 13",
  "device_model": "Pixel 7"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `user_id` | integer (int64) | ✅ | User ID to associate with this device |
| `platform` | string | ✅ | `ANDROID`, `IOS`, or `WEB` |
| `push_token` | string | ✅ | FCM or APNS token (unique) |
| `installation_id` | string | ❌ | Optional installation/device identifier |
| `app_version` | string | ❌ | Application version string |
| `os_version` | string | ❌ | Operating system version |
| `device_model` | string | ❌ | Device model name |

**Platform to Provider Mapping:**
- `ANDROID` → `FCM`
- `IOS` → `APNS`
- `WEB` → `FCM`

#### Success Response (201 Created)
```json
{
  "id": 12,
  "platform": "ANDROID",
  "installation_id": "inst_xyz789",
  "is_active": true,
  "created_at": "2025-07-14T10:00:00Z"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | user_id is required | `user_id` missing or 0 |
| `400` | push_token is required | `push_token` missing or empty |
| `400` | platform is required | `platform` missing or empty |
| `400` | invalid request body | Malformed JSON |
| `400` | invalid platform: must be ANDROID, IOS, or WEB | Invalid platform value |
| `404` | user not found | User does not exist |
| `409` | device token already exists | `push_token` already registered |
| `500` | Internal server error | Database insert failed |

---

### 2.2 Update Device Token
**`PATCH /device-tokens/{id}`**

Updates a device token's metadata. All fields are optional. The `push_token` can be updated, but must be unique.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Device token ID |

#### Request Body (all fields optional)
```json
{
  "platform": "IOS",
  "push_token": "new_fcm_token_xyz789...",
  "installation_id": "inst_new123",
  "app_version": "2.6.0",
  "os_version": "iOS 17",
  "device_model": "iPhone 15",
  "is_active": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `platform` | string | ❌ | `ANDROID`, `IOS`, or `WEB` |
| `push_token` | string | ❌ | New FCM/APNS token (must be unique) |
| `installation_id` | string | ❌ | New installation identifier |
| `app_version` | string | ❌ | New application version |
| `os_version` | string | ❌ | New OS version |
| `device_model` | string | ❌ | New device model |
| `is_active` | boolean | ❌ | Activate or deactivate the token |

If `platform` is updated, `provider` is automatically mapped based on the new platform.

#### Success Response (200 OK)
```json
{
  "message": "device token updated"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid token id | Invalid ID format |
| `400` | invalid request body | Malformed JSON |
| `400` | at least one field required | No fields provided for update |
| `400` | invalid platform: must be ANDROID, IOS, or WEB | Invalid platform value |
| `404` | device token not found | Token does not exist |
| `409` | device token already exists | New `push_token` already used by another device |
| `500` | Internal server error | Database update failed |

---

### 2.3 Delete Device Token
**`DELETE /device-tokens/{id}`**

Permanently deletes a device token. This should be called when a user logs out or uninstalls the app.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Device token ID |

#### Success Response (200 OK)
```json
{
  "message": "deleted"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid token id | Invalid ID format |
| `404` | device token not found | Token does not exist |
| `500` | Internal server error | Database delete failed |

---

## Use Cases & Common Scenarios

### Admin Workflow: View User Details
1. **Search users**: `GET /users/search?keyword=john` → get user IDs
2. **View user details**: `GET /users/{id}` → get full profile
3. **View devices**: `GET /users/{id}/device-tokens` → see all user devices
4. **View segments**: `GET /users/{id}/segments` → check segment memberships
5. **View notification history**: `GET /users/{id}/notifications` → see delivery status

### Mobile App Workflow: Device Registration
1. **Register token**: `POST /device-tokens` → when user logs in
2. **Update token**: `PATCH /device-tokens/{id}` → when app version changes
3. **Delete token**: `DELETE /device-tokens/{id}` → when user logs out

### Admin Workflow: Add User to Segment
1. **Search user**: `GET /users/search?keyword=john` → find user
2. **Add to segment**: `POST /segments/{id}/members` → with `user_id` from search result

---

## Changelog

| Version | Date | Description |
|---------|------|-------------|
| 1.0 | 2026-07-14 | Initial API documentation for Users & Device Tokens feature |
