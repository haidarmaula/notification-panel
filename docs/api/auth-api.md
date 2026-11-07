# Staff authentication API

All three endpoints accept JSON at `/api/v1/auth` behind the server's `X-API-Key` guard. Use the approved private HTTPS ingress. Keep the API key on the trusted server or BFF. The API does not set browser cookies.

The [staff and business OpenAPI contract](../contracts/staff-business.openapi.json) includes request bodies, response shapes and synthetic wire examples for authentication, profile and staff administration, and project business routes.

| Method and path | Request | Success |
| --- | --- | --- |
| `POST /api/v1/auth/login` | `{"email":"staff@example.invalid","password":"…"}` | Opens a new session family and returns an access/refresh pair. |
| `POST /api/v1/auth/refresh` | `{"refresh_token":"…"}` | Consumes the current refresh token and returns both successors in the same family. |
| `POST /api/v1/auth/logout` | `{"refresh_token":"…"}` | Revokes the represented family. Repeated logout with the same signed, unexpired credential returns 200. Other login sessions are independent. |

A successful login or rotation returns HTTP 200 and `Cache-Control: no-store`.

```json
{
  "success": true,
  "message": "success",
  "data": {
    "access_token": "ACCESS_JWT",
    "refresh_token": "REFRESH_JWT"
  }
}
```

Send `Authorization: Bearer ACCESS_JWT` with the API key on protected business endpoints. The current implementation issues access tokens for up to 15 minutes and caps each family at 24 hours from login. Access also ends at family expiry. The API checks current account, session and recovery generation before accepting a token. Password changes, role changes, deactivation and logout invalidate affected access and refresh credentials. Changing a role or enabling an account again requires fresh login. Token role claims are descriptive. Object and operation permissions still come from the database.

Serialize refresh calls per family and atomically replace both credentials with the successful response. Redeeming an already-used refresh token revokes the active family, including its latest access token. If a refresh response is lost or the credential store update fails, discard the pair and log in again. Keep the refresh credential in the trusted BFF session or native secure storage under the approved client policy.

Invalid JSON returns 400, a body above 64 KiB returns 413 and invalid/expired/revoked credentials return 401. Database failures return 500 with `internal server error`. Login and rotation require successful session persistence before credentials are released. A restored database requires a new session recovery epoch and reconciliation before exposure.

## Cutover

Deploy migration 000033 and drain every older server before restarting shared ingress. Tokens issued by the stateless implementation require fresh login. Roll out clients that consume both tokens before enabling the new refresh contract. Downgrading to a stateless server could re-enable legacy credentials. Keep old servers out of service and rotate signing material if a compatible rollback requires it. For database recovery and outstanding deployment decisions, see the [staff session specification](../specs/staff-sessions.md).
