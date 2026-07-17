# Mobile Sync API Documentation

**Version:** 1.0  
**Base URL:** `/api/v1`  
**Content-Type:** `application/json`

---

## Authentication

This endpoint requires:
- **API Key**: Passed in the `X-API-Key` header.

The JWT token passed in the request body is verified using the shared secret with the main backend (Laravel). This ensures that only authenticated users from the main application can sync their data.

---

## Common Error Responses

| Status Code | Message | Description |
|-------------|---------|-------------|
| `400` | Invalid request body | Malformed JSON or validation error |
| `401` | Unauthorized | Invalid or expired JWT |
| `500` | Internal server error | Unexpected server error |

---

## 1. Mobile Sync

### 1.1 Sync User and Device Token
**`POST /mobile/sync`**

Synchronizes a user from the main backend (Laravel) and registers their device token in the notification service.

This endpoint is called by the mobile app after a successful login. It performs the following operations:
1. Verifies the JWT token using the shared secret.
2. Extracts user data (`external_id`, `name`, `email`) from the JWT.
3. Upserts the user in the `users` table (creates if not exists, updates if exists).
4. Registers or updates the device token for that user.

#### Request Body
```json
{
  "jwt": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "device_token": {
    "platform": "ANDROID",
    "push_token": "fcm_token_abc123...",
    "installation_id": "inst_xyz789",
    "app_version": "2.5.0",
    "os_version": "Android 13",
    "device_model": "Pixel 7"
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `jwt` | string | ✅ | JWT token from the main backend (Laravel). Must contain `external_id`, `name`, `email`. |
| `device_token.platform` | string | ✅ | `ANDROID`, `IOS`, or `WEB` |
| `device_token.push_token` | string | ✅ | FCM or APNS token |
| `device_token.installation_id` | string | ❌ | Optional installation/device identifier |
| `device_token.app_version` | string | ❌ | Application version string |
| `device_token.os_version` | string | ❌ | Operating system version |
| `device_token.device_model` | string | ❌ | Device model name |

**Platform to Provider Mapping:**
- `ANDROID` → `FCM`
- `IOS` → `APNS`
- `WEB` → `FCM`

#### Success Response (200 OK)
```json
{
  "user_id": 1,
  "device_token_id": 12
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | jwt is required | `jwt` field missing or empty |
| `400` | push_token is required | `push_token` field missing or empty |
| `400` | platform is required | `platform` field missing or empty |
| `400` | invalid request body | Malformed JSON |
| `400` | invalid platform: must be ANDROID, IOS, or WEB | Invalid platform value |
| `401` | invalid or expired JWT | JWT verification failed (invalid signature, expired, or malformed) |
| `500` | Internal server error | Database or unexpected error |

---

## Mobile App Integration Flow

```text
1. User logs in via Laravel backend.
   ↓
2. Laravel returns JWT token (contains external_id, name, email).
   ↓
3. Mobile app calls POST /api/v1/mobile/sync with:
   - JWT token
   - Device token (FCM/APNS)
   - Platform and metadata
   ↓
4. Notification service verifies JWT using shared secret.
   ↓
5. Notification service upserts user (by external_id).
   ↓
6. Notification service registers/updates device token.
   ↓
7. Returns user_id and device_token_id.
```

---

## JWT Claims Requirement

The JWT must be signed with the shared secret (`MOBILE_JWT_SECRET`) and contain the following claims:

```json
{
  "external_id": "USR001",
  "name": "John Doe",
  "email": "john@example.com",
  "exp": 1700000000,
  "iat": 1700000000
}
```

| Claim | Type | Required | Description |
|-------|------|----------|-------------|
| `external_id` | string | ✅ | Unique user identifier from the main system |
| `name` | string | ✅ | User's full name |
| `email` | string | ✅ | User's email address |
| `exp` | integer | ✅ | Expiration timestamp (Unix) |
| `iat` | integer | ✅ | Issued at timestamp (Unix) |

**Note:** The signing algorithm must be `HS256` (HMAC-SHA256).

---

## Use Case: Mobile App Login

### Step 1: User logs in via Laravel
```http
POST /api/login
{
  "email": "john@example.com",
  "password": "secret"
}
```

Response:
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "user": {
    "external_id": "USR001",
    "name": "John Doe",
    "email": "john@example.com"
  }
}
```

### Step 2: Mobile app sends sync request
```http
POST /api/v1/mobile/sync
X-API-Key: your-api-key

{
  "jwt": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "device_token": {
    "platform": "ANDROID",
    "push_token": "fcm_token_abc123...",
    "app_version": "2.5.0",
    "os_version": "Android 13",
    "device_model": "Pixel 7"
  }
}
```

### Step 3: Notification service responds
```json
{
  "user_id": 1,
  "device_token_id": 12
}
```

---

## Changelog

| Version | Date | Description |
|---------|------|-------------|
| 1.0 | 2025-07-16 | Initial API documentation for Mobile Sync feature |
