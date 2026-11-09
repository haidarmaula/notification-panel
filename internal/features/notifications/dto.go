// Package notifications implements the notification-campaign feature of the
// Notification Panel: the CRUD surface for authoring campaigns, the
// targeting model that selects an audience, the send trigger that hands a
// campaign to the delivery pipeline, and the per-provider adapters that
// dispatch a resolved fan-out to a push provider.
//
// # Scope
//
// A notification campaign is the unit of work in this feature. It is
// authored by a staff user, targets either a single segment, an explicit
// user list, or the whole user base, and is then either sent immediately or
// scheduled for later dispatch. Actual delivery is performed
// asynchronously: the API's Send endpoint publishes a Kafka event, and the
// delivery worker consumes that event and calls the push provider. This
// package owns everything up to the Kafka publish, and the provider
// adapters, but not the worker's consumption loop (see cmd/worker).
//
// # Layers
//
// The package follows the standard feature-module triad used elsewhere in
// this repository:
//
//   - dto.go declares the wire shapes for HTTP requests and responses.
//   - handler.go adapts HTTP to service calls and maps errors to status
//     codes.
//   - service.go implements the business rules: target validation,
//     lifecycle transitions (DRAFT-only edits), scheduling, and the send
//     trigger.
//   - processor.go is the worker-side counterpart that consumes the Kafka
//     event and drives the fan-out.
//   - provider.go declares the NotificationProvider interface; the
//     onesignal_provider.go, fcm_provider.go, and mock_provider.go files
//     implement it.
//   - module.go and routes.go wire the feature into the HTTP server.
//
// # Lifecycle
//
// A notification moves through the states documented in the database
// schema: DRAFT → SCHEDULED → QUEUED → PROCESSING → COMPLETED, with SENT
// and FAILED as terminal variants. This package enforces the portion of
// that lifecycle that is visible to API callers: edits and deletes are
// permitted only in DRAFT; sending is a transition from DRAFT (immediate)
// or from SCHEDULED (via the scheduler); editing a queued or completed
// notification is rejected with ErrNotificationNotDraft.
//
// # Provider abstraction
//
// This package retains a NotificationProvider interface, OneSignal and FCM
// adapters, and a MockProvider used for local/tests. The legacy notifications
// module is not mounted by the current binaries; cmd/worker uses the protected
// daemon and scoped dispatch pipeline instead.
//
// # Concurrency
//
// Every type in this package is safe for concurrent use after construction.
// The handler and service share no per-request mutable state; the provider
// is expected to be safe for concurrent Send calls, and both shipped
// implementations are.
package notifications

import "time"

// CreateNotificationRequest represents request payload for creating a notification.
//
// It is the JSON body expected by POST /api/v1/notifications. The fields
// are not all required: the set of required fields depends on the Type, and
// the handler enforces the cross-field constraints after decoding.
//
// # Type-dependent requirements
//
//   - BROADCAST (or empty, which defaults to BROADCAST): no further fields
//     are required. The campaign targets every active user.
//   - SEGMENT: SegmentID must be non-nil.
//   - INDIVIDUAL: UserIDs must contain at least one entry.
//
// A request that violates the constraint for its Type is rejected with 400
// before the service is invoked.
type CreateNotificationRequest struct {
	// Title is the notification's headline. It is required (non-empty) and
	// is validated by the handler before the service is called. It is
	// passed through to the push provider verbatim.
	Title string `json:"title"`

	// Body is the notification's textual content. It is required
	// (non-empty), validated alongside Title, and passed to the provider
	// verbatim.
	Body string `json:"body"`

	// TemplateID, when non-nil, references a templates row from which the
	// notification was authored. The service checks that the referenced
	// template exists (ErrTemplateNotFound otherwise) but does not copy the
	// template's content into the notification — Title and Body are used
	// as-is. The field is therefore metadata for reporting and audit, not a
	// content source.
	TemplateID *int64 `json:"template_id,omitempty"`

	// Type selects the targeting strategy. Accepted values are "BROADCAST",
	// "SEGMENT", and "INDIVIDUAL"; an empty string defaults to BROADCAST in
	// the handler. Any other value is rejected by the service with
	// ErrInvalidTargetType and served as a 400.
	Type string `json:"type"` // BROADCAST, SEGMENT, INDIVIDUAL

	// SegmentID is required when Type == "SEGMENT" and ignored otherwise.
	// The service checks that the referenced segment exists
	// (ErrSegmentNotFound) at create time; it does not pre-resolve the
	// segment's membership, which is done by the worker at send time.
	SegmentID *int64 `json:"segment_id,omitempty"`

	// UserIDs is required (non-empty) when Type == "INDIVIDUAL" and ignored
	// otherwise. The service does not currently validate that the IDs
	// correspond to existing users; a send targeting a nonexistent user ID
	// will simply produce no delivery for that ID.
	UserIDs []int64 `json:"user_ids,omitempty"`

	// ScheduledAt, when non-nil, marks the notification for future
	// dispatch rather than immediate send. The service validates that the
	// timestamp is in the future (ErrInvalidScheduledTime); a past value is
	// rejected. Whether the notification is queued immediately or held for
	// the scheduler depends on the client's follow-up — creating with
	// ScheduledAt set places the row in DRAFT, and the send trigger is what
	// promotes it.
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
}

// UpdateNotificationRequest represents request payload for updating a notification.
//
// It is the JSON body expected by PATCH /api/v1/notifications/{id}. Every
// field is a pointer so that the client can distinguish "leave this field
// unchanged" (field omitted, pointer nil) from "set this field to a new
// value" (field present). This is the standard PATCH semantics: an absent
// field is not an implicit zero.
//
// # Which fields can be updated
//
// Only the four listed fields. Target type, segment ID, and user list are
// immutable after creation — changing a campaign's audience after the fact
// would produce a different campaign, so the design treats those fields as
// fixed. To retarget, the client creates a new notification.
//
// # Update preconditions
//
// The service accepts updates only on notifications in DRAFT status. A
// non-DRAFT row is rejected with ErrNotificationNotDraft, mapped to 409
// Conflict by the handler. At least one field must be present; the handler
// rejects an entirely-empty body with a 400 before calling the service.
type UpdateNotificationRequest struct {
	// Title, when non-nil, replaces the notification's title. An empty
	// string is accepted as a valid replacement — the service does not
	// re-apply the non-empty check that Create performs.
	Title *string `json:"title,omitempty"`

	// Body, when non-nil, replaces the notification's body. As with Title,
	// an empty string is accepted.
	Body *string `json:"body,omitempty"`

	// TemplateID, when non-nil, replaces the notification's template
	// reference. The service verifies that the new template exists.
	TemplateID *int64 `json:"template_id,omitempty"`

	// ScheduledAt, when non-nil, replaces the scheduled dispatch time. The
	// service validates that the new timestamp is in the future.
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
}

// NotificationListItem represents a notification in a list view.
//
// It is the row shape emitted by the list endpoint, deliberately narrower
// than NotificationDetail so that a list of hundreds of notifications does
// not carry the body text or statistics of each. The fields not present
// here — Body, Template, Statistics — are available via the detail
// endpoint.
type NotificationListItem struct {
	// ID is the notification's internal primary key.
	ID int64 `json:"id"`

	// Title is the notification's headline.
	Title string `json:"title"`

	// Type is the targeting strategy as resolved from the notification's
	// target rows. Both the filtered and unfiltered list queries now
	// compute this value and default to "BROADCAST" only when no target
	// row exists, so the field reflects the actual targeting strategy in
	// list responses.
	Type string `json:"type"`

	// Status is the current lifecycle state — DRAFT, SCHEDULED, QUEUED,
	// PROCESSING, COMPLETED, SENT, or FAILED.
	Status string `json:"status"`

	// CreatedBy is the display name of the staff user who authored the
	// notification, joined in at query time. It is not the numeric staff
	// ID; see NotificationDetail.CreatedBy for the ID-carrying variant.
	CreatedBy string `json:"created_by"`

	// ScheduledAt is the intended dispatch time, or nil for an immediate
	// send that has not been scheduled.
	ScheduledAt *time.Time `json:"scheduled_at"`

	// SentAt is the time at which dispatch completed, or nil for a
	// notification that has not been sent.
	SentAt *time.Time `json:"sent_at"`

	// CreatedAt is the row's creation timestamp.
	CreatedAt time.Time `json:"created_at"`
}

// TemplateBrief represents a template in notification detail.
//
// It is a compact reference used inside NotificationDetail, carrying only
// the fields the detail view displays when a template is associated with
// the notification. A full template record is available from the templates
// feature.
type TemplateBrief struct {
	// ID is the templates.id the notification references.
	ID int64 `json:"id"`

	// Name is the template's display name.
	Name string `json:"name"`
}

// StaffBrief represents a staff user in notification detail.
//
// It is a compact reference used inside NotificationDetail to identify the
// staff user who created the notification. Unlike NotificationListItem,
// which flattens the creator to a name string, this struct preserves the
// numeric ID so that the detail view can link to the staff user's profile.
type StaffBrief struct {
	// ID is the staff_users.id of the creator.
	ID int64 `json:"id"`

	// Name is the creator's display name.
	Name string `json:"name"`
}

// NotificationStatistics represents delivery statistics.
//
// It is an aggregate view over the notification's delivery rows, computed
// by the detail query. The three counters are not mutually exclusive in the
// way they might first appear: Delivered counts rows whose state reached
// DELIVERED, Opened counts rows that subsequently reached OPENED, and a row
// that was delivered and then opened contributes to both counters. Targeted
// is the total row count, including rows that failed or are still pending.
//
// As a consequence, Delivered + Opened is not the total of anything
// meaningful; consumers who want a delivery rate should use Delivered /
// Targeted, and an open rate should use Opened / Delivered.
type NotificationStatistics struct {
	// Targeted is the total number of delivery rows created for this
	// notification — one per (user, device) pair at fan-out time.
	Targeted int64 `json:"targeted"`

	// Delivered is the number of rows whose state reached DELIVERED.
	Delivered int64 `json:"delivered"`

	// Opened is the number of rows whose state reached OPENED. It is a
	// subset of Delivered for practical purposes, but the schema does not
	// enforce that invariant, so a corrupt or buggy sequence of events
	// could in principle produce Opened > Delivered.
	Opened int64 `json:"opened"`
}

// NotificationDetail represents full notification detail.
//
// It is the response shape of GET /api/v1/notifications/{id}, carrying the
// complete campaign record plus its joined creator, template, and delivery
// statistics. It is deliberately wider than NotificationListItem so that
// the list endpoint can remain cheap.
//
// # Nullable timestamps
//
// Four of the timestamp fields are pointers because they are populated by
// lifecycle transitions that a DRAFT or SCHEDULED notification has not yet
// undergone. The pattern is: ScheduledAt is set at scheduling time, SentAt
// at dispatch completion, PublishedAt when the campaign is queued for the
// worker, and CompletedAt when all deliveries have reached a terminal
// state. A nil value means "not yet reached that stage", not "unset by
// mistake".
type NotificationDetail struct {
	// ID is the notification's internal primary key.
	ID int64 `json:"id"`

	// Title is the notification's headline.
	Title string `json:"title"`

	// Body is the notification's textual content.
	Body string `json:"body"`

	// Template is the associated template's compact reference, or nil if
	// the notification was authored without one.
	Template *TemplateBrief `json:"template,omitempty"`

	// Type is the targeting strategy.
	Type string `json:"type"`

	// Status is the current lifecycle state.
	Status string `json:"status"`

	// CreatedBy is the staff user who authored the notification, carrying
	// both the numeric ID and the display name.
	CreatedBy StaffBrief `json:"created_by"`

	// ScheduledAt is the intended dispatch time, or nil.
	ScheduledAt *time.Time `json:"scheduled_at"`

	// SentAt is the dispatch completion time, or nil.
	SentAt *time.Time `json:"sent_at"`

	// PublishedAt is the time the notification was queued for the worker,
	// or nil.
	PublishedAt *time.Time `json:"published_at"`

	// CompletedAt is the time all deliveries reached a terminal state, or
	// nil.
	CompletedAt *time.Time `json:"completed_at"`

	// CreatedAt is the row's creation timestamp.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the row's last-modified timestamp.
	UpdatedAt time.Time `json:"updated_at"`

	// Statistics is the aggregate delivery view. It is always present —
	// not a pointer — so a notification with no deliveries reports zero
	// counts rather than a nil object.
	Statistics NotificationStatistics `json:"statistics"`
}

// ListNotificationsResponse represents paginated list response.
//
// It is the response shape of GET /api/v1/notifications. The Pagination
// field carries metadata about the full result set, not just the current
// page, so a client can render "page 3 of 12" without a second request.
type ListNotificationsResponse struct {
	// Data is the current page of notifications. An empty page is not
	// distinguishable at this level from "no notifications exist" — the
	// client must inspect Pagination.Total to tell the cases apart.
	Data []NotificationListItem `json:"data"`

	// Pagination is the pagination metadata for the full result set.
	Pagination Pagination `json:"pagination"`
}

// Pagination holds pagination metadata.
//
// It is embedded in every list response in this feature and describes the
// position of the current page within the full filtered result set. The
// struct is deliberately minimal: it does not expose a total-pages count,
// which the client can compute from Total and Limit.
type Pagination struct {
	// Page is the 1-indexed page number. A value of 0 would indicate a
	// caller error, but the handler defaults to 1 when the query
	// parameter is absent or unparseable, so a legitimate response never
	// contains 0.
	Page int32 `json:"page"`

	// Limit is the maximum number of items per page. The handler defaults
	// it to 10 and clamps it to 100; an out-of-range client value is
	// silently replaced with the default, not rejected. The value here
	// reflects what was actually applied, not what the client asked for.
	Limit int32 `json:"limit"`

	// Total is the total number of rows matching the current filter,
	// before pagination was applied.
	Total int64 `json:"total"`
}

// CreateNotificationResponse represents response after creating a notification.
//
// It is the response body of POST /api/v1/notifications. It carries only
// the identity and initial status of the created row; the full detail is
// available via a follow-up GET.
type CreateNotificationResponse struct {
	// ID is the internal primary key of the newly created notification.
	ID int64 `json:"id"`

	// Status is the initial lifecycle state. It is "DRAFT" for a
	// notification created without a ScheduledAt, and may be "SCHEDULED"
	// for one created with a future ScheduledAt — the exact transition is
	// the service's responsibility, not documented at this layer.
	Status string `json:"status"`
}
