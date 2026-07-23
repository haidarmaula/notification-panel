# Segments API Documentation

**Version:** 1.0  
**Base URL:** `/api/v1`  
**Content-Type:** `application/json`

---

## Authentication

All endpoints require authentication via:
- **API Key**: Passed in the `X-API-Key` header.
- **JWT Token**: Passed in the `Authorization: Bearer <token>` header.

The authenticated staff ID is extracted from the JWT and used as the `created_by` field when creating resources.

---

## Common Error Responses

| Status Code | Message | Description |
|-------------|---------|-------------|
| `400` | Invalid request body | Malformed JSON or validation error |
| `401` | Unauthorized | Missing or invalid API Key / JWT |
| `404` | Resource not found | The requested resource does not exist |
| `500` | Internal server error | Unexpected server error |

---

## 1. Segments

### 1.1 List Segments
**`GET /segments`**

Retrieves a paginated list of segments with optional search by name.

#### Query Parameters
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `page` | integer | 1 | Page number |
| `limit` | integer | 10 | Items per page (max 100) |
| `search` | string | - | Keyword to filter segments by name (case-insensitive) |

#### Success Response (200 OK)
```json
{
  "data": [
    {
      "id": 1,
      "name": "Premium Users",
      "description": "Users with premium subscription",
      "created_by": "Admin",
      "member_count": 150,
      "created_at": "2025-07-01T10:00:00Z",
      "updated_at": "2025-07-01T10:00:00Z"
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
| `500` | Internal server error | Database query failed |

---

### 1.2 Get Segment by ID
**`GET /segments/{id}`**

Retrieves detailed information about a specific segment, including member count and creator details.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Segment ID |

#### Success Response (200 OK)
```json
{
  "id": 1,
  "name": "Premium Users",
  "description": "Users with premium subscription",
  "created_by": {
    "id": 5,
    "name": "Admin"
  },
  "member_count": 150,
  "created_at": "2025-07-01T10:00:00Z",
  "updated_at": "2025-07-01T10:00:00Z"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid segment id | Invalid ID format |
| `404` | segment not found | Segment does not exist |
| `500` | Internal server error | Database query failed |

---

### 1.3 Create Segment
**`POST /segments`**

Creates a new segment. The segment name must be unique.

#### Request Body
```json
{
  "name": "Premium Users",
  "description": "Users with premium subscription"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | ✅ | Unique segment name |
| `description` | string | ❌ | Optional segment description |

#### Success Response (201 Created)
```json
{
  "id": 1
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | name is required | `name` is missing or empty |
| `400` | invalid request body | Malformed JSON |
| `401` | unauthorized | Invalid or missing authentication |
| `409` | segment name already exists | Segment name is already taken |
| `500` | Internal server error | Database insert failed |

---

### 1.4 Update Segment
**`PATCH /segments/{id}`**

Updates an existing segment's name and/or description. If updating the name, the new name must be unique.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Segment ID |

#### Request Body
```json
{
  "name": "Premium Plus Users",
  "description": "Users with premium plus subscription"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | ❌ | New segment name (must be unique) |
| `description` | string | ❌ | New segment description |

#### Success Response (200 OK)
```json
{
  "message": "segment updated"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid segment id | Invalid ID format |
| `400` | at least one field required | No fields provided for update |
| `400` | invalid request body | Malformed JSON |
| `404` | segment not found | Segment does not exist |
| `409` | segment name already exists | New name is already taken |
| `500` | Internal server error | Database update failed |

---

### 1.5 Delete Segment
**`DELETE /segments/{id}`**

Deletes a segment. **Allowed only if the segment has no members** (`member_count = 0`).

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Segment ID |

#### Success Response (200 OK)
```json
{
  "message": "deleted"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid segment id | Invalid ID format |
| `404` | segment not found | Segment does not exist |
| `409` | cannot delete segment that has members | Segment still has members |
| `500` | Internal server error | Database delete failed |

---

## 2. Segment Members

### 2.1 List Segment Members
**`GET /segments/{id}/members`**

Retrieves a paginated list of users belonging to a specific segment.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Segment ID |

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
      "id": 1,
      "user_id": 10,
      "name": "John Doe",
      "email": "john@example.com",
      "created_at": "2025-07-01T10:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 10,
    "total": 150
  }
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid segment id | Invalid ID format |
| `404` | segment not found | Segment does not exist |
| `500` | Internal server error | Database query failed |

---

### 2.2 Add Member to Segment
**`POST /segments/{id}/members`**

Adds an existing user to a segment. Prevents duplicate memberships.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Segment ID |

#### Request Body
```json
{
  "user_id": 10
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `user_id` | integer (int64) | ✅ | ID of the user to add |

#### Success Response (201 Created)
```json
{
  "message": "member added"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid segment id | Invalid segment ID format |
| `400` | user_id is required | `user_id` missing or 0 |
| `400` | invalid request body | Malformed JSON |
| `404` | segment not found | Segment does not exist |
| `404` | user not found | User does not exist |
| `409` | user is already a member of this segment | Duplicate membership |
| `500` | Internal server error | Database insert failed |

---

### 2.3 Remove Member from Segment
**`DELETE /segments/{id}/members/{userId}`**

Removes a user from a segment. The user must currently be a member.

#### Path Parameters
| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | integer (int64) | Segment ID |
| `userId` | integer (int64) | User ID |

#### Success Response (200 OK)
```json
{
  "message": "member removed"
}
```

#### Error Responses
| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid segment id | Invalid segment ID format |
| `400` | invalid user id | Invalid user ID format |
| `404` | segment not found | Segment does not exist |
| `404` | user not found | User does not exist |
| `404` | user is not a member of this segment | User is not in the segment |
| `500` | Internal server error | Database delete failed |

---

## Changelog

| Version | Date | Description |
|---------|------|-------------|
| 1.0 | 2026-07-07 | Initial API documentation for Segments feature |
