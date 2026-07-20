# Authentication API Documentation

**Version:** 1.0  
**Base URL:** `/api/v1`  
**Content-Type:** `application/json`

---

## Authentication

All endpoints in this module require:
- **API Key**: Passed in the `X-API-Key` header.

JWT tokens are not required for login or refresh endpoints, as they are used to obtain or renew tokens.

---

## Common Error Responses

| Status Code | Message | Description |
|-------------|---------|-------------|
| `400` | Invalid request body | Malformed JSON or validation error |
| `401` | Unauthorized | Invalid credentials or token |
| `500` | Internal server error | Unexpected server error |

---

## 1. Login

**`POST /auth/login`**

Authenticates a staff user using email and password, and returns access and refresh tokens.

### Request Body

```json
{
  "email": "admin@example.com",
  "password": "secure_password"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `email` | string | ✅ | Staff email address |
| `password` | string | ✅ | Staff password |

### Success Response (200 OK)

```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
}
```

| Field | Type | Description |
|-------|------|-------------|
| `access_token` | string | Short-lived JWT for API authorization |
| `refresh_token` | string | Long-lived token to obtain new access tokens |

### Error Responses

| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid request body | Malformed JSON |
| `401` | invalid email or password | Invalid credentials or inactive account |
| `500` | internal server error | Unexpected server error |

---

## 2. Refresh Token

**`POST /auth/refresh`**

Exchanges a valid refresh token for a new access token. The refresh token itself remains valid and can be reused.

### Request Body

```json
{
  "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `refresh_token` | string | ✅ | The refresh token obtained from login |

### Success Response (200 OK)

```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
}
```

| Field | Type | Description |
|-------|------|-------------|
| `access_token` | string | New short-lived JWT for API authorization |

### Error Responses

| Status | Message | Condition |
|--------|---------|-----------|
| `400` | invalid request body | Malformed JSON |
| `401` | invalid or expired token | Refresh token is invalid, expired, or account inactive |
| `500` | internal server error | Unexpected server error |

---

## Token Usage

After successful login, include the access token in the `Authorization` header for all protected endpoints:

```
Authorization: Bearer <access_token>
```

When the access token expires (usually after a short time), use the refresh token to obtain a new one without re-entering credentials. The refresh token has a longer lifetime and should be stored securely on the client side.

---

## Changelog

| Version | Date | Description |
|---------|------|-------------|
| 1.0 | 2025-07-20 | Initial API documentation for Authentication feature |

---

**Note:** Both endpoints require the API Key header (`X-API-Key`) as they are considered internal service endpoints.
