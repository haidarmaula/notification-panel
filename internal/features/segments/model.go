package segments

import "time"

// Actor is a compact reference to a staff user used inside the Segment
// domain model.
//
// It carries only the two fields the segment views display: the internal
// staff ID and the display name. The name "Actor" is broader than the
// actual role — the field is used to identify the *creator* of a segment,
// not an arbitrary actor on a request — but it is the name the rest of the
// segments package uses in the domain layer. Note that the DTO layer uses
// a differently-named struct (StaffBrief) with the same field shape; the
// two are declared separately because the domain model and the wire shape
// have historically been kept apart.
//
// Actor is a plain data struct with no internal locking. It is constructed
// on the stack by the service and passed by value; no synchronisation is
// required.
type Actor struct {
	// ID is the internal staff_users.id of the referenced user.
	ID int64

	// Name is the staff user's display name. It is joined in at query time
	// and reflects the current name on the staff row, not a snapshot taken
	// when the segment was created.
	Name string
}

// Segment represents the domain model for a user segment.
//
// It is the service-layer view of a segments row, joined with the creating
// staff user's identity. The struct is used only within this package and is
// not serialised directly; the wire shapes are SegmentListItem and
// SegmentDetail in dto.go.
//
// # Description is not a pointer
//
// Unlike the DTO's Description field, the domain model declares Description
// as a plain string. This means the domain model cannot represent the
// distinction between "no description" (SQL NULL) and "empty description"
// (empty string) that the wire format's *string preserves at the storage
// layer. When the handler takes the address of this field to populate the
// DTO's pointer — as both List and GetByID do — the resulting pointer is
// always non-nil. See the handler's List method for the client-facing
// consequence.
type Segment struct {
	// ID is the segment's internal primary key.
	ID int64

	// Name is the segment's display name.
	Name string

	// Description is the segment's description, or an empty string when no
	// description is set. The storage-layer NULL-versus-empty distinction
	// is not preserved by this field.
	Description string

	// CreatedBy is a compact reference to the staff user who created the
	// segment.
	CreatedBy Actor

	// CreatedAt is the row's creation timestamp.
	CreatedAt time.Time

	// UpdatedAt is the row's last-modified timestamp.
	UpdatedAt time.Time
}

// SegmentWithCount extends Segment with computed member count.
//
// It is the return type of the service's GetByID method and of the list
// path's underlying query. The member count is a read-time aggregate over
// the segment_members table; it reflects membership changes as soon as
// they commit and is not a cached value.
//
// # Embedding
//
// The Segment is embedded by value, so a SegmentWithCount can be passed
// anywhere a Segment is expected. Field promotion makes the Segment's
// fields accessible directly on a SegmentWithCount value (for example,
// swc.Name reads the embedded Segment's Name). The embedding is the reason
// the list and detail handler methods can construct their DTOs without a
// nested field access.
type SegmentWithCount struct {
	Segment
	MemberCount int64
}

// Member represents a segment member domain model.
//
// It is the service-layer view of a row in the segment_members table,
// joined with the member user's name and email. As with the other domain
// models in this package, it is not serialised directly; the wire shape is
// MemberListItem in members_dto.go.
//
// # Name and Email are flattened from nullable columns
//
// The underlying query returns the member's name and email as pgtype.Text
// values, which may be NULL if the user row has no name or email. The
// service flattens those nullable values to plain strings by reading the
// .String field of the pgtype value — a NULL name becomes the empty
// string. The domain model therefore does not distinguish "user has no
// name" from "user's name is the empty string"; the client sees "" either
// way.
type Member struct {
	// ID is the segment_members.id of the membership row, not the user's
	// ID. The two are distinct: a user may be a member of many segments,
	// and each membership has its own ID.
	ID int64

	// UserID is the internal users.id of the member.
	UserID int64

	// Name is the member user's display name, or an empty string if the
	// underlying column is NULL.
	Name string

	// Email is the member user's email, or an empty string if the
	// underlying column is NULL.
	Email string

	// CreatedAt is the timestamp at which the membership row was created,
	// not the user's own creation timestamp.
	CreatedAt time.Time
}
