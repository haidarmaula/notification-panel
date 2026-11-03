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

## Read, replace and delete

GET on the collection uses the same cursor contract as project user reads.
GET on /campaigns/{campaign_id} returns a campaign and its scoped targets.
Both reads require notifications.read, an explicit grant and an ACTIVE
project. The collection exposes id, title and status. The detail adds body,
template_id, template_name and targets. IDs are decimal strings. Existing
UPLOAD targets may appear in detail as stored references.

PUT on /campaigns/{campaign_id} replaces title, body, template reference and
the full target set using the same input as POST. It returns 200 with id
and status. Omitted or null template_id clears the reference and snapshot.
DELETE on that path requires an empty body and returns 204.

Writes require the same authority as POST. Only DRAFT is writable. A draft
with delivery/read records or unassigned targets returns 409 to preserve
those records. Missing or foreign campaigns return 404. Replacement and
deletion are atomic with their audit event.
