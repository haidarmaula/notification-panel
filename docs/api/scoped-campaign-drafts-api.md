# Create a project campaign draft

`POST /api/v1/apps/{app_id}/campaigns` requires staff API key, staff JWT,
`notifications.draft.write`, an explicit project grant and an ACTIVE project.
An authorized PROVISIONING project returns 409.

The JSON object requires nonblank title and body, bounded to 255 and 4096
UTF-8 bytes respectively. The optional template_id is a positive decimal
string or null. An active template supplies a reference and name snapshot.
The request supplies the final title and body.

Supply 1–100 unique targets. Each target is an object with type and, when
required, a positive decimal string id. BROADCAST takes no ID and must be the
only target. SEGMENT takes a segment ID. INDIVIDUAL takes a user ID. Invalid
input returns 400. Missing or foreign references and inactive templates return 404.
The route accepts no query parameters.

A 201 response contains the string id and DRAFT status in the existing
response envelope. The campaign, targets and audit event commit together.
Scheduling and provider execution are separate operations.

Creation uses the schema defaults for priority and payload. Within the write
transaction, the service locks the project before rechecking staff authority,
locks the optional template, inserts the draft, then validates and inserts
each target. The scoped foreign keys enforce project ownership. A failure
rolls back the draft and any targets already inserted.
