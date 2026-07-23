# Profile API Documentation

**Version:** 1.0  
**Base URL:** `/api/v1`  
**Content-Type:** `application/json`

---

## Authentication

All endpoints require authentication via:
- **API Key**: Passed in the `X-API-Key` header.
- **JWT Token**: Passed in the `Authorization: Bearer <token>` header.

The authenticated staff ID is extracted from the JWT and used to identify the profile owner. Users can only access and modify their own profile.

---

## Common Error Responses

| Status Code | Message | Description |
|-------------|---------|-------------|
| `400` | Invalid request body | Malformed JSON or validation error |
| `401` | Unauthorized | Missing or invalid API Key / JWT |
| `404` | Resource not found | Profile does not exist |
| `409` | Conflict | Email already used by another account |
| `500` | Internal server error | Unexpected server error |

---

## Profile Object

| Field | Type | Description |
|-------|------|-------------|
| `id` | integer | Staff user ID |
| `role_id` | integer | Role ID assigned to the staff |
| `role_name` | string | Name of the assigned role |
| `name` | string | Staff full name |
| `email` | string | Staff email address |
| `is_active` | boolean | Whether the account is active |
| `created_at` | timestamp | Account creation timestamp |
| `updated_at` | timestamp | Last update timestamp |

---

## 1. Get Profile

**`GET /profile`**

Retrieves the profile of the currently authenticated staff user.

### Request Headers
| Header | Value | Required | Description |
|--------|-------|----------|-------------|
| `X-API-Key` | string | ✅ | API Key for service authentication |
| `Authorization` | `Bearer <token>` | ✅ | JWT access token |

### Success Response (200 OK)

```json
{
  "id": 1,
  "role_id": 1,
  "role_name": "SUPER_ADMIN",
  "name": "Admin User",
  "email": "admin@example.com",
  "is_active": true,
  "created_at": "2025-07-01T10:00:00Z",
  "updated_at": "2025-07-01T10:00:00Z"
}
```

### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `401` | unauthorized | Missing or invalid JWT token |
| `404` | profile not found | Staff user does not exist |
| `500` | Internal server error | Database query failed |

---

## 2. Update Profile

**`PATCH /profile`**

Updates the name and/or email of the currently authenticated staff user. At least one field must be provided.

### Request Headers
| Header | Value | Required | Description |
|--------|-------|----------|-------------|
| `X-API-Key` | string | ✅ | API Key for service authentication |
| `Authorization` | `Bearer <token>` | ✅ | JWT access token |

### Request Body
```json
{
  "name": "Updated Name",
  "email": "newemail@example.com"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | ❌ | New staff name (at least one of `name` or `email` required) |
| `email` | string | ❌ | New staff email (must be unique if changed) |

### Success Response (200 OK)

```json
{
  "id": 1,
  "role_id": 1,
  "role_name": "SUPER_ADMIN",
  "name": "Updated Name",
  "email": "newemail@example.com",
  "is_active": true,
  "created_at": "2025-07-01T10:00:00Z",
  "updated_at": "2025-07-20T12:00:00Z"
}
```

### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | at least one field (name or email) must be provided | Both `name` and `email` are empty |
| `400` | invalid request body | Malformed JSON |
| `401` | unauthorized | Missing or invalid JWT token |
| `404` | profile not found | Staff user does not exist |
| `409` | email already used by another account | New email is already taken |
| `500` | Internal server error | Database update failed |

---

## 3. Update Password

**`PATCH /profile/password`**

Changes the password for the currently authenticated staff user. The current password must be verified before the new password is set.

### Request Headers
| Header | Value | Required | Description |
|--------|-------|----------|-------------|
| `X-API-Key` | string | ✅ | API Key for service authentication |
| `Authorization` | `Bearer <token>` | ✅ | JWT access token |

### Request Body
```json
{
  "current_password": "old_secure_password",
  "new_password": "new_secure_password"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `current_password` | string | ✅ | Current password for verification |
| `new_password` | string | ✅ | New password to set |

### Success Response (200 OK)

```json
{
  "message": "password updated successfully"
}
```

### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | current password and new password are required | Missing `current_password` or `new_password` |
| `400` | invalid request body | Malformed JSON |
| `401` | unauthorized | Missing or invalid JWT token |
| `401` | current password is incorrect | Provided current password does not match |
| `404` | profile not found | Staff user does not exist |
| `500` | Internal server error | Password update failed |

---

## Changelog

| Version | Date | Description |
|---------|------|-------------|
| 1.0 | 2026-07-20 | Initial API documentation for Profile feature |
