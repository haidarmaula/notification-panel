package audit

// Staff-administration actions recorded in the audit log.
//
// Every value in this group corresponds to a state-changing operation on a
// staff_users row. They are stored verbatim in the audit_logs.action column
// and are part of the persisted schema — changing a value is a breaking
// change for any downstream analytics or log-search tooling that filters on
// it.
const (
	// ACTION_STAFF_CREATE is recorded when a new staff user is provisioned
	// via the staff administration API.
	ACTION_STAFF_CREATE = "STAFF_CREATE"

	// ACTION_STAFF_UPDATE is recorded when a staff user's profile fields
	// (name, email, role) are modified.
	ACTION_STAFF_UPDATE = "STAFF_UPDATE"

	// ACTION_STAFF_UPDATE_STATUS is recorded when a staff user is activated
	// or deactivated (status field change).
	ACTION_STAFF_UPDATE_STATUS = "STAFF_UPDATE_STATUS"

	// ACTION_STAFF_UPDATE_PASSWORD is recorded when a staff user's password
	// is changed. The before/after payload must never include password
	// material.
	ACTION_STAFF_UPDATE_PASSWORD = "STAFF_UPDATE_PASSWORD"

	// ACTION_STAFF_DELETE is recorded when a staff user is removed.
	ACTION_STAFF_DELETE = "STAFF_DELETE"
)

// Notification-campaign actions recorded in the audit log.
//
// These capture the full lifecycle of a notification campaign from draft
// creation through dispatch. ACTION_NOTIFICATION_SEND is the only action in
// this group that triggers an outbound push; the rest are metadata-only
// changes on a campaign that has not yet been dispatched.
const (
	// ACTION_NOTIFICATION_CREATE is recorded when a new notification campaign
	// is created in DRAFT status.
	ACTION_NOTIFICATION_CREATE = "NOTIFICATION_CREATE"

	// ACTION_NOTIFICATION_UPDATE is recorded when a draft notification's
	// content, targeting, or schedule is modified.
	ACTION_NOTIFICATION_UPDATE = "NOTIFICATION_UPDATE"

	// ACTION_NOTIFICATION_DELETE is recorded when a notification campaign is
	// removed. Deletion is only permitted while the campaign is still DRAFT.
	ACTION_NOTIFICATION_DELETE = "NOTIFICATION_DELETE"

	// ACTION_NOTIFICATION_SEND is recorded when a staff user triggers
	// immediate dispatch of a notification, transitioning it to QUEUED.
	ACTION_NOTIFICATION_SEND = "NOTIFICATION_SEND"
)

// Segment-management actions recorded in the audit log.
//
// Segments are audience definitions used for notification targeting; changes
// to a segment can alter the recipient set of any future notification that
// references it, so every mutation is audited.
const (
	// ACTION_SEGMENT_CREATE is recorded when a new segment is defined.
	ACTION_SEGMENT_CREATE = "SEGMENT_CREATE"

	// ACTION_SEGMENT_UPDATE is recorded when a segment's name, filter, or
	// membership rules are modified.
	ACTION_SEGMENT_UPDATE = "SEGMENT_UPDATE"

	// ACTION_SEGMENT_DELETE is recorded when a segment is removed.
	ACTION_SEGMENT_DELETE = "SEGMENT_DELETE"
)

// Entity-type discriminators used in the audit_logs.entity_type column.
//
// These values classify what kind of object an action targeted, allowing
// consumers to filter the audit trail by domain without parsing the action
// string. They are stored verbatim and are part of the persisted schema.
const (
	// ENTITY_TYPE_STAFF identifies audit entries whose subject is a
	// staff_users row.
	ENTITY_TYPE_STAFF = "staff_user"

	// ENTITY_TYPE_SEGMENT identifies audit entries whose subject is a
	// segments row.
	ENTITY_TYPE_SEGMENT = "segment"

	// ENTITY_TYPE_NOTIFICATION identifies audit entries whose subject is a
	// notifications row.
	ENTITY_TYPE_NOTIFICATION = "notification"
)

// Application registry action/entity values are persisted schema. No registry
// HTTP services are wired yet; metadata/grant mutations will own atomic audits.
const (
	ACTION_APPLICATION_CREATE       = "APPLICATION_CREATE"
	ACTION_APPLICATION_UPDATE       = "APPLICATION_UPDATE"
	ACTION_APPLICATION_GRANT_ENABLE = "APPLICATION_GRANT_ENABLE"
	ACTION_APPLICATION_GRANT_REVOKE = "APPLICATION_GRANT_REVOKE"
	ENTITY_TYPE_APPLICATION         = "application"
	ENTITY_TYPE_APPLICATION_GRANT   = "application_grant"
)

const (
	ACTION_SEGMENT_MEMBER_ADD    = "SEGMENT_MEMBER_ADD"
	ACTION_SEGMENT_MEMBER_REMOVE = "SEGMENT_MEMBER_REMOVE"
	ENTITY_TYPE_SEGMENT_MEMBER   = "segment_member"
)

const (
	ACTION_TEMPLATE_CREATE = "TEMPLATE_CREATE"
	ACTION_TEMPLATE_UPDATE = "TEMPLATE_UPDATE"
	ENTITY_TYPE_TEMPLATE   = "template"
)

const ACTION_NOTIFICATION_SCHEDULE = "NOTIFICATION_SCHEDULE"

// ACTION_NOTIFICATION_REPLAY_REQUEST records a scoped operational request header.
// It does not indicate broker acceptance or provider/device delivery.
const ACTION_NOTIFICATION_REPLAY_REQUEST = "NOTIFICATION_REPLAY_REQUEST"
