# Staff API Documentation

**Version:** 1.0  
**Base URL:** `/api/v1`  
**Content-Type:** `application/json`

---

## Authentication

All endpoints require authentication via:
- **API Key**: Passed in the `X-API-Key` header.
- **JWT Token**: Passed in the `Authorization: Bearer <token>` header.

**Role Requirement:** All staff management endpoints are restricted to users with the **SUPER_ADMIN** role. Regular staff cannot access these endpoints.

The authenticated staff ID is extracted from the JWT and used for authorization checks.

---

## Common Error Responses

| Status Code | Message | Description |
|-------------|---------|-------------|
| `400` | Invalid request body | Malformed JSON or validation error |
| `401` | Unauthorized | Missing or invalid API Key / JWT |
| `403` | Forbidden | Insufficient permissions (not SUPER_ADMIN) or self-deletion attempt |
| `404` | Resource not found | Staff user does not exist |
| `409` | Conflict | Email already registered |
| `500` | Internal server error | Unexpected server error |

---

## Staff Object

| Field | Type | Description |
|-------|------|-------------|
| `id` | integer | Staff user ID |
| `role_id` | integer | Role ID assigned |
| `role_name` | string | Role name (e.g., SUPER_ADMIN, ADMIN, OPERATOR) |
| `name` | string | Staff full name |
| `email` | string | Staff email address (unique) |
| `is_active` | boolean | Whether the account is active |
| `created_at` | timestamp | Account creation timestamp |
| `updated_at` | timestamp | Last update timestamp |

---

## 1. List Staff

**`GET /staff`**

Retrieves a paginated list of staff users with optional search by name or email.

### Query Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `page` | integer | 1 | Page number |
| `limit` | integer | 10 | Items per page (max 100) |
| `search` | string | - | Keyword to search by name or email (case‑insensitive) |

### Success Response (200 OK)

```json
{
  "data": [
    {
      "id": 1,
      "role_id": 1,
      "role_name": "SUPER_ADMIN",
      "name": "Admin User",
      "email": "admin@example.com",
      "is_active": true,
      "created_at": "2025-07-01T10:00:00Z",
      "updated_at": "2025-07-01T10:00:00Z"
    },
    {
      "id": 2,
      "role_id": 2,
      "role_name": "OPERATOR",
      "name": "Operator User",
      "email": "operator@example.com",
      "is_active": true,
      "created_at": "2025-07-02T10:00:00Z",
      "updated_at": "2025-07-02T10:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 10,
    "total": 2
  }
}
```

### Error Responses

| Status | Message | Condition |
|--------|---------|-----------|
| `500` | Internal server error | Database query failed |

---

## 2. Get Staff by ID

**`GET /staff/{id}`**

Retrieves detailed information about a specific staff user.

### Path Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Staff user ID |

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
| `400` | invalid id | Invalid ID format |
| `404` | staff user not found | Staff does not exist |
| `500` | Internal server error | Database query failed |

---

## 3. Create Staff

**`POST /staff`**

Creates a new staff user. The email must be unique and the role name must exist in the `roles` table.

### Request Body

```json
{
  "role": "ADMIN",
  "name": "New Staff",
  "email": "newstaff@example.com",
  "password": "secure_password"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `role` | string | ✅ | Role name (must exist in roles table) |
| `name` | string | ✅ | Staff full name |
| `email` | string | ✅ | Staff email (must be unique) |
| `password` | string | ✅ | Password (will be hashed) |

### Success Response (201 Created)

```json
{
  "id": 3,
  "role_id": 2,
  "role_name": "ADMIN",
  "name": "New Staff",
  "email": "newstaff@example.com",
  "is_active": true,
  "created_at": "2025-07-20T12:00:00Z",
  "updated_at": "2025-07-20T12:00:00Z"
}
```

### Error Responses

| Status | Message | Condition |
|--------|---------|-----------|
| `400` | missing required fields | Role, name, email, or password missing |
| `400` | invalid request body | Malformed JSON |
| `400` | invalid role | Role name does not exist |
| `409` | email already registered | Email is already used by another staff |
| `500` | Internal server error | Database insert failed |

---

## 4. Update Staff

**`PATCH /staff/{id}`**

Updates a staff user's role, name, or email. At least one field must be provided.

### Path Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Staff user ID |

### Request Body

```json
{
  "role": "OPERATOR",
  "name": "Updated Name",
  "email": "newemail@example.com"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `role` | string | ❌ | New role name (must exist if provided) |
| `name` | string | ❌ | New staff name |
| `email` | string | ❌ | New staff email (must be unique if changed) |

### Success Response (200 OK)

```json
{
  "id": 3,
  "role_id": 3,
  "role_name": "OPERATOR",
  "name": "Updated Name",
  "email": "newemail@example.com",
  "is_active": true,
  "created_at": "2025-07-20T12:00:00Z",
  "updated_at": "2025-07-20T12:30:00Z"
}
```

### Error Responses

| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid id | Invalid ID format |
| `400` | at least one field must be provided | No fields provided for update |
| `400` | invalid request body | Malformed JSON |
| `400` | invalid role | Role name does not exist |
| `404` | staff user not found | Staff does not exist |
| `409` | email already used by another staff | New email is already taken |
| `500` | Internal server error | Database update failed |

---

## 5. Update Staff Status

**`PATCH /staff/{id}/status`**

Updates the active status of a staff user (activate or deactivate).

### Path Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Staff user ID |

### Request Body

```json
{
  "is_active": false
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `is_active` | boolean | ✅ | New active status (true = active, false = inactive) |

### Success Response (200 OK)

```json
{
  "id": 3,
  "role_id": 3,
  "role_name": "OPERATOR",
  "name": "Updated Name",
  "email": "newemail@example.com",
  "is_active": false,
  "created_at": "2025-07-20T12:00:00Z",
  "updated_at": "2025-07-20T12:35:00Z"
}
```

### Error Responses

| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid id | Invalid ID format |
| `400` | invalid request body | Malformed JSON |
| `404` | staff user not found | Staff does not exist |
| `500` | Internal server error | Database update failed |

---

## 6. Update Staff Password

**`PATCH /staff/{id}/password`**

Updates the password of a staff user.

### Path Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Staff user ID |

### Request Body

```json
{
  "password": "new_secure_password"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `password` | string | ✅ | New password (will be hashed) |

### Success Response (200 OK)

```json
{
  "message": "password updated"
}
```

### Error Responses

| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid id | Invalid ID format |
| `400` | password required | Password field missing or empty |
| `400` | invalid request body | Malformed JSON |
| `404` | staff user not found | Staff does not exist |
| `500` | Internal server error | Password update failed |

---

## 7. Delete Staff

**`DELETE /staff/{id}`**

Permanently deletes a staff user from the system.

**Important:** Administrators cannot delete their own account (self-deletion is forbidden).

### Path Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Staff user ID |

### Request Body

No request body.

### Success Response (200 OK)

```json
{
  "message": "staff user deleted"
}
```

### Error Responses

| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid id | Invalid ID format |
| `403` | cannot delete your own account | Actor attempted to delete their own account |
| `404` | staff user not found | Staff does not exist |
| `500` | Internal server error | Database delete failed |

---

## Changelog

| Version | Date | Description |
|---------|------|-------------|
| 1.0 | 2026-07-20 | Initial API documentation for Staff Management feature |
| 1.0 | 2026-07-23 | Added DELETE /staff/{id} endpoint with self-deletion prevention |
