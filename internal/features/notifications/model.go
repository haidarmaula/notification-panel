package notifications

// NotificationStatus defines notification status.
//
// # Current state of this type
//
// The constants declared below do not cover the full set of status values
// the codebase actually uses. The delivery processor (processor.go) reads
// and writes the raw strings "QUEUED", "PROCESSING", and "COMPLETED"
// directly, none of which appears here, and StatusSending ("SENDING") is
// declared but never referenced anywhere in the package. The scheduler
// (cmd/scheduler) promotes rows to "QUEUED" and the processor transitions
// through "PROCESSING" to "COMPLETED", but the canonical vocabulary for
// those states lives in SQL literals rather than in this file.
//
// The practical consequence is that callers cannot rely on this type as the
// authoritative source of valid status values: a switch on NotificationStatus
// that covers every declared constant will still not cover the states the
// processor observes. A future revision should either add the missing
// constants and use them throughout, or remove the type in favour of the
// string literals the rest of the code already uses.
//
// Because the type is a string alias, the JSON and SQL representations are
// the raw string values; the typed constants are a compile-time convenience
// only, and no validation is performed on values read from the database.
type NotificationStatus string

const (
	// StatusDraft is the initial state of a newly created notification. It
	// is the only state in which the notification is editable; any update
	// attempt after this state is rejected with ErrNotificationNotDraft.
	StatusDraft NotificationStatus = "DRAFT"

	// StatusScheduled is intended to represent a notification whose
	// scheduled_at time has been set and which awaits promotion by the
	// scheduler. In the current codebase this constant is declared but not
	// referenced: rows transition from DRAFT to "QUEUED" via the scheduler's
	// conditional SQL update rather than through the value declared here.
	StatusScheduled NotificationStatus = "SCHEDULED"

	// StatusSending is declared but not referenced anywhere in this
	// package. The processor uses the literal "PROCESSING" to represent the
	// same intent; this constant and that literal do not agree, and the
	// processor is the one that runs. Do not rely on this value being
	// written to the database.
	StatusSending NotificationStatus = "SENDING"

	// StatusSent represents a notification whose dispatch has completed
	// successfully. As with the other constants in this block, the
	// processor does not write this value; it writes "COMPLETED" instead.
	StatusSent NotificationStatus = "SENT"

	// StatusFailed represents a notification whose dispatch failed. This is
	// the one constant in the block that the processor also uses — its
	// failure paths write the literal "FAILED", which happens to match.
	// The processor's success paths, however, write "COMPLETED", which
	// does not match any constant here.
	StatusFailed NotificationStatus = "FAILED"
)

// TargetType defines target type.
//
// # Completeness
//
// The three constants declared below cover every target type the processor
// successfully expands. A fourth value, "UPLOAD", is present in the
// database and handled by the processor's expandTargets method as an
// explicit no-op — it is not represented here, and a notification whose
// only target is UPLOAD will be marked COMPLETED with zero deliveries.
//
// As with NotificationStatus, the type is a string alias; validation is the
// responsibility of the create path (which rejects unknown values via
// ErrInvalidTargetType) and of the target repository, not of this type.
type TargetType string

const (
	// TargetBroadcast selects every active user. The processor resolves it
	// by paginating through ListByStatus("ACTIVE", ...) and is subject to
	// the pagination limits documented on Processor.expandTargets.
	TargetBroadcast TargetType = "BROADCAST"

	// TargetSegment selects the members of a single segment, identified by
	// the target row's segment_id. A target row of this type whose
	// segment_id is NULL is silently skipped by the processor's
	// expandTargets method rather than treated as an error.
	TargetSegment TargetType = "SEGMENT"

	// TargetIndividual selects an explicit list of user IDs. The processor
	// reads a single UserID per target row, so a multi-user INDIVIDUAL
	// target is represented as multiple rows, not as one row carrying a
	// list.
	TargetIndividual TargetType = "INDIVIDUAL"
)
