# Project templates

Staff API key, JWT, an explicit project grant and ACTIVE status are required.
GET uses notifications.read. POST and PUT use notifications.draft.write.

| Method | Path under /api/v1/apps/{app_id} | Success |
| --- | --- | --- |
| GET | /templates | 200, cursor page |
| GET | /templates/{template_id} | 200, template |
| POST | /templates | 201, template |
| PUT | /templates/{template_id} | 200, replacement template |

Templates expose id as a decimal string, name, title_template, body_template
and is_active. Lists include inactive templates and follow the project user
cursor contract. Detail and writes reject query parameters.

POST and PUT require all four mutable fields. Names follow registry name
validation. Title and body must be nonblank and fit 255 and 4096 UTF-8 bytes
respectively. Invalid input returns 400. Missing and foreign templates return
404. Name is a display label and may repeat. Set is_active to false to retire
a template while preserving existing campaign references. Campaign draft
creation and replacement require active templates when assigning a reference.
Writes preserve legacy variable metadata and commit with a scoped audit event.
