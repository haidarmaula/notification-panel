package audit

// LogParams describes a single audit-log entry to be persisted by
// AuditService.Log.
//
// The struct is intentionally transport-agnostic: callers populate it from
// whichever domain context they are in (staff creation, notification send,
// etc.) and the service is responsible for serialising Before and After into
// JSON and for merging in actor metadata pulled from the request context.
//
// Fields with a zero value are handled distinctly:
//
//   - EntityName and EntityID of zero cause the corresponding database
//     columns to be written as NULL rather than as empty/zero values, so
//     callers that create an entity before its ID is known can leave them
//     unset.
//   - Before and After are optional; when nil, the corresponding JSON column
//     is left NULL rather than being written as the four-byte literal
//     "null". This keeps the audit table compact for create/delete events
//     where only one of the two states exists.
//
// LogParams is a plain data struct with no internal locking. It is safe to
// construct, populate, and pass to Log within a single goroutine; sharing an
// instance across goroutines requires external synchronisation.
type LogParams struct {
	// Action is the verb describing what happened. Use one of the
	// ACTION_* constants declared in this package; free-form values are
	// accepted by the database but will not be understood by downstream
	// audit consumers.
	Action string

	// EntityType classifies the subject of the action. Use one of the
	// ENTITY_TYPE_* constants. It is stored verbatim in the audit_logs
	// table.
	EntityType string

	// EntityName is the human-readable name of the affected entity (e.g.
	// a segment's display name). Optional: an empty string results in a
	// SQL NULL rather than an empty-string value.
	EntityName string

	// EntityID is the numeric primary key of the affected entity. Optional:
	// a zero value results in a SQL NULL, which is useful for create events
	// where the ID has not yet been assigned. Note that 0 is treated as
	// "unset" — audit entries for entities whose real ID is 0 are not
	// representable.
	EntityID int64

	// Before is the entity state before the action, serialised to JSON by
	// AuditService.Log. Nil indicates "no prior state" (typically a create
	// event) and causes the column to be written as NULL. Staff entities
	// require StaffSnapshot (or a pointer); password resets forbid snapshots.
	// Other entities accept JSON-marshalable values. Unsafe/invalid payloads
	// cause Log to return an error before any database write occurs.
	Before any

	// After is the entity state after the action, serialised to JSON by
	// AuditService.Log. Nil indicates "no subsequent state" (typically a
	// delete event). See Before for encoding semantics.
	After any
}
