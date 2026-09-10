package segments

// AddMemberRequest represents request payload for adding a user to a segment.
//
// It is the JSON body expected by POST /api/v1/segments/{id}/members. The
// segment ID is taken from the URL path, not from the body; this struct
// carries only the target user ID.
type AddMemberRequest struct {
	// UserID is the internal users.id of the user to add. It is required
	// and must be non-zero; the handler rejects a zero value with a 400
	// before invoking the service. Note that the field is not marked
	// omitempty and is not a pointer, so a request body that omits it
	// entirely decodes as 0 and is caught by the same check.
	UserID int64 `json:"user_id"`
}

// MemberListItem represents a segment member in a list view.
//
// It is the row shape emitted by the members list endpoint. It carries the
// membership's own identity, the target user's identity and profile fields,
// and the membership creation timestamp.
//
// # CreatedAt is a string, not a time.Time
//
// This field is declared as a string and is populated by the handler with
// a hand-formatted timestamp rather than by relying on encoding/json's
// default marshalling for time.Time. The format used is
// "2006-01-02T15:04:05Z", which resembles RFC3339 but is not identical to
// it: the trailing Z in the layout is a *literal* Z, not Go's Z07:00
// timezone indicator. The output therefore always ends with "Z" — for a
// UTC timestamp that happens to be correct, but for a non-UTC timestamp
// the output claims UTC while showing the local time. See the handler's
// ListMembers method for the full mechanics.
//
// This diverges from the profile package's ProfileResponse, which formats
// with time.RFC3339 — a genuinely timezone-aware layout. The segments
// members endpoint is currently the only place in the codebase that uses a
// literal-Z format.
type MemberListItem struct {
	// ID is the segment_members.id of the membership row, not the user's
	// own ID. A client that needs the user's ID should read UserID.
	ID int64 `json:"id"`

	// UserID is the internal users.id of the member user.
	UserID int64 `json:"user_id"`

	// Name is the member user's display name. When the underlying user
	// row's name column is NULL, this is the empty string.
	Name string `json:"name"`

	// Email is the member user's email. When the underlying column is
	// NULL, this is the empty string.
	Email string `json:"email"`

	// CreatedAt is the membership creation timestamp, formatted by the
	// handler as a string. See the type-level doc for the format caveat.
	CreatedAt string `json:"created_at"`
}

// ListMembersResponse represents paginated list of segment members.
//
// It is the response shape of GET /api/v1/segments/{id}/members. As with
// the segment list response, Pagination carries metadata about the full
// member set, not just the current page.
type ListMembersResponse struct {
	// Data is the current page of members. An empty page is not
	// distinguishable at this level from "the segment has no members" —
	// the client must inspect Pagination.Total to tell the cases apart.
	Data []MemberListItem `json:"data"`

	// Pagination is the pagination metadata for the full member set.
	Pagination Pagination `json:"pagination"`
}
