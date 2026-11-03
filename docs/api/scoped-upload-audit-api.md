# Project upload and audit history

All routes require staff API key, JWT, explicit project grant and ACTIVE
status. They use the project user cursor contract.

| GET path under /api/v1/apps/{app_id} | Permission | Item fields |
| --- | --- | --- |
| /uploads | notifications.read | id, total_rows, valid_rows, invalid_rows, created_at |
| /uploads/{batch_id}/rows | notifications.read | id, is_valid, created_at |
| /audit-logs | audit.read | id, actor_id, action, entity_type, entity_id, created_at |

Audit access is limited to ADMIN and SUPER_ADMIN. IDs are decimal strings
and entity_id may be null. The audit page contains metadata for records
assigned to the project. Upload paths expose historical batch summaries and
row validity. Missing or foreign batches return 404. These are read endpoints.
