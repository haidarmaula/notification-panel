// Package segments implements the audience-segment feature of the
// Notification Panel: the CRUD surface for defining named groups of users,
// the membership view, and the read paths that support segment-based
// notification targeting.
//
// # Scope
//
// A segment is a labelled audience — a name, an optional description, and
// an implicit set of member users — that a notification campaign can target
// instead of an explicit user list. This package owns the segment record
// itself and its metadata; membership (which users belong to which segment)
// is stored in the segment_members table and is managed out-of-band by the
// operations team or a future reconciliation job. The notification worker
// consumes segments read-only during fan-out.
//
// # What this package owns versus what it does not
//
// The package exposes endpoints for creating, reading, updating, and
// deleting segment records. It does not expose endpoints for adding or
// removing individual members — those operations belong to a future
// membership-management feature, or to administrative tooling that is out
// of scope for the current API surface. A segment can be created through
// this API and will be empty until membership is populated by other means.
//
// # Layering
//
// The package follows the standard feature-module triad used elsewhere in
// this repository, with one structural difference:
//
//   - dto.go declares the wire shapes for HTTP requests and responses.
//   - model.go declares the domain views of a segment and its member count.
//   - interfaces.go declares the repository contracts the service depends
//     on. Unlike every other feature module in the codebase, this package
//     depends on interfaces rather than concrete repository types — see
//     that file for the rationale.
//   - service.go implements the business logic.
//   - handler.go adapts HTTP to service calls and maps errors to status
//     codes.
//   - helpers.go contains small parsing and context-extraction utilities
//     used by the handler.
//   - module.go and routes.go wire the feature into the HTTP server.
//
// # Concurrency
//
// Every type in this package is safe for concurrent use after construction.
// The handler shares a single *SegmentService, which holds no per-request
// state.
package segments

import "time"

// CreateSegmentRequest represents the request payload for creating a segment.
//
// It is the JSON body expected by POST /api/v1/segments. Only Name is
// required; Description is optional. There is no cross-field validation:
// the service accepts any combination of the two fields that satisfies the
// handler's presence check on Name.
type CreateSegmentRequest struct {
	// Name is the segment's display name. It is required and must be
	// non-empty; the handler rejects an empty value with a 400 before
	// invoking the service. Names are expected to be unique across the
	// table; a collision is reported by the service as ErrSegmentNameTaken
	// and served as 409 by the handler.
	Name string `json:"name"`

	// Description is an optional human-readable note about the segment's
	// purpose. Nil when omitted from the request, in which case the
	// persisted column is NULL. An empty string is treated the same as
	// omitted by the JSON decoder only if the field is present as
	// `"description": ""`; the two cases are otherwise distinguishable
	// because the pointer is nil for omitted and non-nil for empty.
	Description *string `json:"description,omitempty"`
}

// UpdateSegmentRequest represents the request payload for updating a segment.
//
// It is the JSON body expected by PATCH /api/v1/segments/{id}. Both fields
// are pointers so that the decoder can distinguish "field omitted" (nil)
// from "field explicitly set". The handler requires at least one field to
// be non-nil; a request with both nil is rejected with a 400.
//
// As with the notifications feature's update DTO, the pointers give this
// request true PATCH semantics — a caller updating only the name does not
// have to know the current description to avoid overwriting it.
type UpdateSegmentRequest struct {
	// Name, when non-nil, replaces the segment's name. The service
	// verifies that the new name does not collide with an existing
	// segment's name; a collision produces ErrSegmentNameTaken, served as
	// 409. An empty string is accepted as a valid replacement by the
	// service (the non-empty check performed by Create is not re-applied
	// on update).
	Name *string `json:"name,omitempty"`

	// Description, when non-nil, replaces the segment's description. An
	// empty string is a valid replacement and results in an empty-string
	// column value rather than NULL — the two are not equivalent at the
	// storage layer.
	Description *string `json:"description,omitempty"`
}

// SegmentListItem represents a segment in a list view.
//
// It is the row shape emitted by the list endpoint. It carries the
// segment's own fields plus a computed member count and a compact reference
// to the creating staff user. The list view does not expose membership
// details or audit history; those are available from the detail endpoint or
// from future membership-management endpoints.
//
// # Field identity with SegmentDetail
//
// This struct is currently field-identical to SegmentDetail. The two are
// declared separately so that their wire shapes can diverge — for example,
// if the list view later omits the description or the detail view adds
// membership counts by status. As long as they remain identical, a future
// maintainer may be tempted to unify them; doing so would foreclose that
// divergence and should be a deliberate decision rather than a casual
// refactor.
type SegmentListItem struct {
	// ID is the segment's internal primary key.
	ID int64 `json:"id"`

	// Name is the segment's display name.
	Name string `json:"name"`

	// Description is the segment's optional description. In the current
	// handler implementation this field is always populated — the handler
	// takes the address of the domain model's Description string, which
	// produces a non-nil pointer even when the value is empty. A client
	// therefore always sees a `description` key, potentially with an
	// empty value, rather than the key being absent. See the handler's
	// List method for the details.
	Description *string `json:"description,omitempty"`

	// CreatedBy is a compact reference to the staff user who created the
	// segment, carrying both the internal ID and the display name.
	CreatedBy StaffBrief `json:"created_by"`

	// MemberCount is the number of users currently in the segment, as
	// computed by the underlying query. It is a read-time aggregate, not
	// a cached value, so it reflects membership changes as soon as they
	// commit. Because the segment_members table is expected to be
	// maintained out-of-band, this count reflects the last reconciliation
	// rather than live attribute-based membership.
	MemberCount int64 `json:"member_count"`

	// CreatedAt is the segment's creation timestamp.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the segment's last-modified timestamp.
	UpdatedAt time.Time `json:"updated_at"`
}

// SegmentDetail represents full segment detail.
//
// It is the response shape of GET /api/v1/segments/{id}. As noted on
// SegmentListItem, the two structs are currently field-identical; see that
// documentation for why they remain separate.
type SegmentDetail struct {
	// ID is the segment's internal primary key.
	ID int64 `json:"id"`

	// Name is the segment's display name.
	Name string `json:"name"`

	// Description is the segment's optional description. As with
	// SegmentListItem.Description, the current handler always populates
	// this field with a non-nil pointer, so the key is always present in
	// the JSON body.
	Description *string `json:"description,omitempty"`

	// CreatedBy is a compact reference to the creating staff user.
	CreatedBy StaffBrief `json:"created_by"`

	// MemberCount is the number of users currently in the segment.
	MemberCount int64 `json:"member_count"`

	// CreatedAt is the segment's creation timestamp.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the segment's last-modified timestamp.
	UpdatedAt time.Time `json:"updated_at"`
}

// StaffBrief represents a staff user in segment detail.
//
// It is a compact reference used in both SegmentListItem.CreatedBy and
// SegmentDetail.CreatedBy, carrying only the fields the segment views
// display. A full staff record is available from the staff feature.
//
// The name "StaffBrief" is reused across features — the notifications
// package declares a struct with the same name and the same fields — but
// each package declares its own type rather than sharing one. This keeps
// the packages decoupled at the cost of duplicate declarations; a future
// shared DTO package could collapse them.
type StaffBrief struct {
	// ID is the staff_users.id of the referenced user.
	ID int64 `json:"id"`

	// Name is the staff user's display name.
	Name string `json:"name"`
}

// ListSegmentsResponse represents paginated list response.
//
// It is the response shape of GET /api/v1/segments. The Pagination field
// carries metadata about the full result set, not just the current page.
type ListSegmentsResponse struct {
	// Data is the current page of segments. An empty page is not
	// distinguishable at this level from "no segments exist" — the
	// client must inspect Pagination.Total to tell the cases apart.
	Data []SegmentListItem `json:"data"`

	// Pagination is the pagination metadata for the full result set.
	Pagination Pagination `json:"pagination"`
}

// Pagination holds pagination metadata.
//
// It is field-identical to the Pagination struct declared in the
// notifications package, and to the one in the users package if that
// feature has one. As with StaffBrief, each package declares its own copy
// rather than sharing a common DTO.
type Pagination struct {
	// Page is the 1-indexed page number. The handler defaults to 1 when
	// the query parameter is absent or unparseable, so a legitimate
	// response never contains 0.
	Page int32 `json:"page"`

	// Limit is the maximum number of items per page. The handler defaults
	// it to 10 and caps it at 100; an out-of-range client value is
	// silently replaced with the default, not rejected. The value here
	// reflects what was actually applied, not what the client asked for.
	Limit int32 `json:"limit"`

	// Total is the total number of rows matching the current filter,
	// before pagination was applied.
	Total int64 `json:"total"`
}

// CreateSegmentResponse represents response after creating a segment.
//
// It is the response body of POST /api/v1/segments. It carries only the
// new segment's ID; a client that needs the full record must issue a
// follow-up GET. The response's message is "segment created", which is
// where the created/updated distinction is conveyed to the client.
type CreateSegmentResponse struct {
	// ID is the internal primary key of the newly created segment.
	ID int64 `json:"id"`
}
