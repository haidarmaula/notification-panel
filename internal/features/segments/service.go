package segments

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"

	"hello/internal/audit"
	"hello/internal/database/sqlc"

	"github.com/jackc/pgx/v5/pgtype"
)

// Domain errors.
//
// These sentinels are returned by SegmentService methods and translated
// into HTTP status codes by SegmentHandler. They are declared here, in the
// file that owns their methods, rather than in a separate errors.go; the
// members sub-resource's sentinels live in members_service.go.
var (
	// ErrStaffNotFoundOrInactive is returned when the request context does
	// not carry a staff identity, or when the identified staff user cannot
	// be loaded from the database. It is the same value declared in the
	// notifications and profile packages, but each package declares its
	// own sentinel — a future consolidation into a shared error package
	// would collapse them.
	//
	// The handler maps this to 401 Unauthorized.
	ErrStaffNotFoundOrInactive = errors.New("staff account not found or inactive")

	// ErrSegmentNotFound is returned when the requested segment ID does
	// not resolve to a row. It is returned only when the repository reports
	// pgx.ErrNoRows; any other lookup failure is wrapped and surfaced as a
	// non-sentinel error so the handler can map it to 500 rather than 404.
	//
	// The handler maps this to 404 Not Found.
	ErrSegmentNotFound = errors.New("segment not found")

	// ErrSegmentNameTaken is returned by Create and Update when the
	// proposed name collides with an existing segment's name. The check
	// that produces it — segmentRepo.ExistsByName — is not atomic with
	// the subsequent write, so a concurrent create or rename can race past
	// it; see Create and Update for the details.
	//
	// The handler maps this to 409 Conflict.
	ErrSegmentNameTaken = errors.New("segment name already exists")

	// ErrSegmentHasMembers is returned by Delete when the segment still
	// has members in the segment_members table. The check is a
	// count-before-delete, not an atomic guard, and it is enforced by the
	// service rather than by a schema constraint; a concurrent add can
	// race past it. See Delete for the details.
	//
	// The handler maps this to 409 Conflict.
	ErrSegmentHasMembers = errors.New("cannot delete segment that has members")
)

// SegmentService provides business logic for segment management.
//
// It is the CRUD service for segment records: creating, reading, updating,
// and deleting segments and their metadata. Membership management — adding
// and removing users — is handled by a separate MembersService; the two
// types share the same segment and member repositories but are wired
// independently and do not call each other.
//
// # Dependencies
//
// The service is constructed with four collaborators, declared as
// interfaces rather than concrete repository types:
//
//   - segmentRepo — the SegmentRepository interface, providing segment
//     CRUD and name-existence checks.
//   - memberRepo — the SegmentMemberRepository interface, providing the
//     member counts that decorate list and detail responses.
//   - staffRepo — the StaffUserRepository interface, providing the
//     created-by staff lookup used to populate the Actor reference.
//   - auditService — the single-method AuditService interface, for
//     recording administrative actions.
//
// All four must be non-nil. The interface-typed dependencies are the
// structural difference between this service and MembersService, which
// depends on concrete repository types; see interfaces.go for the
// rationale.
//
// # Concurrency
//
// The service is stateless after construction and safe for concurrent use.
// It holds no per-request state and shares nothing mutable between
// invocations. The interface-typed repositories are expected to be
// concurrency-safe; the concrete implementations backed by pgxpool are.
type SegmentService struct {
	segmentRepo  SegmentRepository
	memberRepo   SegmentMemberRepository
	staffRepo    StaffUserRepository
	auditService AuditService
}

// NewSegmentService creates a new SegmentService instance.
//
// All four parameters must be non-nil; none is validated, so a nil
// repository or audit service will panic on the first method call that
// uses it rather than at construction. In practice the dependencies are
// wired by NewSegmentModule from the shared sqlc.Queries and the shared
// audit service.
//
// # Interface-typed parameters
//
// Because the parameters are declared as interfaces, a caller is free to
// pass either the concrete repository types from
// internal/database/repository or a test fake. The module passes the
// concrete types; a unit test can pass fakes without a live database.
func NewSegmentService(
	segmentRepo SegmentRepository,
	memberRepo SegmentMemberRepository,
	staffRepo StaffUserRepository,
	auditService AuditService,
) *SegmentService {
	return &SegmentService{
		segmentRepo:  segmentRepo,
		memberRepo:   memberRepo,
		staffRepo:    staffRepo,
		auditService: auditService,
	}
}

// List returns a paginated list of segments with optional search.
//
// # Flow
//
// The method takes one of two branches depending on whether search is
// empty. Each branch fetches a page of segment rows, maps them to domain
// Segment values (joined with the creating staff user's name), and returns
// them. After the branch, the method counts the members of each returned
// segment to produce the SegmentWithCount wrapper.
//
//   - When search is empty, the unfiltered List query is used.
//   - When search is non-empty, the Search query is used, matching against
//     the segment's name (and possibly other text fields, per the SQL).
//
// The two branches are functionally identical apart from which repository
// method they call and produce the same domain shape.
//
// # Incorrect total on the search path
//
// The returned total is computed via segmentRepo.Count(ctx) — the
// *unfiltered* row count — regardless of whether a search was applied.
// A caller searching for a rare name will therefore receive a page of 0
// or 1 items together with a total that reflects the entire segments
// table. A client building "page X of Y" pagination from these two values
// will render incorrect page counts on any filtered list. The correct
// total on the search path would require a search-aware count query; no
// such query is currently used.
//
// # Swallowed member-count errors
//
// The member count for each segment is fetched via a per-segment
// CountBySegment call whose error is discarded:
//
//	memberCount, _ := s.memberRepo.CountBySegment(ctx, seg.ID)
//
// A transient database error during a count is therefore reported to the
// client as MemberCount == 0, indistinguishable from a segment that
// genuinely has no members. The list is still returned successfully, so
// the failure is invisible unless the client has another source of truth
// for the count.
//
// # N+1 query behaviour
//
// Member counts are fetched one segment at a time inside a loop. For a
// page of N segments, the method issues N additional COUNT queries after
// the initial page fetch and the total count — a classic N+1 pattern. For
// the default page size of 10 this is 10 extra round-trips; for a client
// that requests limit=100 it is 100. A batched count query (returning
// counts for all page IDs in one statement) would resolve the pattern;
// the current schema and queries do not provide one.
//
// # Errors
//
// Returns a wrapped error with the prefix "search segments: ", "list
// segments: ", or "count segments: " for failures of the corresponding
// repository call. None of these is a sentinel, so the handler serves
// them through its default 500 branch, and the wrapped message reaches
// the client verbatim. The per-segment member-count errors are not
// surfaced at all — see above.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *SegmentService) List(ctx context.Context, page, limit int32, search string) ([]SegmentWithCount, int64, error) {
	offset := (page - 1) * limit

	var segments []Segment
	var err error

	if search != "" {
		rows, err := s.segmentRepo.Search(ctx, search, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("search segments: %w", err)
		}
		segments = make([]Segment, len(rows))
		for i, row := range rows {
			segments[i] = Segment{
				ID:          row.ID,
				Name:        row.Name,
				Description: row.Description.String,
				CreatedBy: Actor{
					ID:   row.CreatedBy,
					Name: row.CreatedByName,
				},
				CreatedAt: row.CreatedAt.Time,
				UpdatedAt: row.UpdatedAt.Time,
			}
		}
	} else {
		rows, err := s.segmentRepo.List(ctx, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("list segments: %w", err)
		}
		segments = make([]Segment, len(rows))
		for i, row := range rows {
			segments[i] = Segment{
				ID:          row.ID,
				Name:        row.Name,
				Description: row.Description.String,
				CreatedBy: Actor{
					ID:   row.CreatedBy,
					Name: row.CreatedByName,
				},
				CreatedAt: row.CreatedAt.Time,
				UpdatedAt: row.UpdatedAt.Time,
			}
		}
	}

	total, err := s.segmentRepo.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count segments: %w", err)
	}

	result := make([]SegmentWithCount, len(segments))
	for i, seg := range segments {
		memberCount, _ := s.memberRepo.CountBySegment(ctx, seg.ID)
		result[i] = SegmentWithCount{
			Segment:     seg,
			MemberCount: memberCount,
		}
	}

	return result, total, nil
}

// GetByID returns a single segment with member count as domain object.
//
// # Flow
//
//  1. Load the segment row by ID. A missing row returns
//     ErrSegmentNotFound; any other lookup failure is wrapped with
//     "get segment: " and returned as a non-sentinel error.
//  2. Fetch the segment's current member count. A failure here is wrapped
//     with the prefix "count members: " and returned as a non-sentinel
//     error.
//  3. Look up the creating staff user to obtain the creator's display
//     name. A failure here is *swallowed*: the method proceeds with an
//     empty name.
//
// # Creator ID is zeroed on staff-lookup failure
//
// The creator lookup at step 3 is implemented as:
//
//	actor, err := s.staffRepo.FindByID(ctx, seg.CreatedBy)
//	actorName := ""
//	if err == nil {
//	    actorName = actor.Name
//	}
//
// On failure, actor is the zero value of sqlc.StaffUser. The subsequent
// construction reads actor.ID regardless of whether the lookup succeeded:
//
//	CreatedBy: Actor{
//	    ID:   actor.ID,   // 0 when the lookup failed
//	    Name: actorName,
//	},
//
// The result is that a transient or permanent staff-lookup failure
// produces a response whose creator reference is {id: 0, name: ""},
// discarding the correct creator ID held in seg.CreatedBy. A client that
// uses the ID for subsequent lookups will attempt to fetch staff user 0.
// The correct fallback would set ID to seg.CreatedBy (which is always
// available) and only fall back to an empty Name. This pattern repeats in
// Create and Update.
//
// # Errors
//
// Returns ErrSegmentNotFound only when the segment lookup produces
// pgx.ErrNoRows. Other lookup failures are wrapped with
// "get segment: ". Returns a wrapped "count members: " error for the
// member-count failure. The staff-lookup failure does not produce an
// error — see the section above — so the caller cannot detect it from the
// return values alone.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *SegmentService) GetByID(ctx context.Context, id int64) (*SegmentWithCount, error) {
	seg, err := s.segmentRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSegmentNotFound
		}
		return nil, fmt.Errorf("get segment: %w", err)
	}

	memberCount, err := s.memberRepo.CountBySegment(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("count members: %w", err)
	}

	actor, err := s.staffRepo.FindByID(ctx, seg.CreatedBy)
	actorName := ""
	if err == nil {
		actorName = actor.Name
	}

	return &SegmentWithCount{
		Segment: Segment{
			ID:          seg.ID,
			Name:        seg.Name,
			Description: seg.Description.String,
			CreatedBy: Actor{
				ID:   actor.ID,
				Name: actorName,
			},
			CreatedAt: seg.CreatedAt.Time,
			UpdatedAt: seg.UpdatedAt.Time,
		},
		MemberCount: memberCount,
	}, nil
}

// CreateParams holds input for creating a segment.
//
// It is the service-layer counterpart of CreateSegmentRequest. The handler
// is responsible for decoding the wire request and injecting the acting
// staff user's ID into CreatedBy. The service then verifies that a staff
// user with that ID exists and writes the row.
//
// The struct is a plain data value, constructed on the stack by the
// handler and passed by value.
type CreateParams struct {
	// Name is the segment's display name. It is required; the handler
	// enforces non-emptiness before calling Create. The service checks
	// for uniqueness against the segment table and rejects a collision
	// with ErrSegmentNameTaken.
	Name string

	// Description is an optional human-readable note. Nil means "no
	// description set", which the service translates to a SQL NULL; a
	// non-nil pointer to an empty string produces an empty-string column
	// value. The two are distinguishable at the storage layer.
	Description *string

	// CreatedBy is the staff user ID recorded as the segment's creator.
	// The service verifies that a staff user with this ID exists, but
	// does *not* verify that it matches the acting staff user from the
	// request context — see Create for the details.
	CreatedBy int64
}

// Create creates a new segment and returns the domain Segment.
//
// # Flow
//
//  1. Verify the acting staff identity from the request context via
//     audit.GetStaffID. A missing identity, or a staff row that cannot be
//     loaded, produces ErrStaffNotFoundOrInactive.
//  2. Check that params.Name is not already taken via
//     segmentRepo.ExistsByName. A collision produces ErrSegmentNameTaken.
//  3. Translate params.Description to a pgtype.Text — NULL when the
//     pointer is nil, an empty string when it points to an empty string.
//  4. Insert the segment row with params.CreatedBy recorded as the
//     creator.
//  5. Write an audit entry. An audit failure is logged and does not
//     abort the operation.
//  6. Look up the creating staff user's display name for the response.
//  7. Return the domain Segment.
//
// # CreatedBy is trusted, not derived from context
//
// The service verifies the *acting* staff user (from the JWT claims via
// audit.GetStaffID) but writes the *supplied* params.CreatedBy into the
// row. In the current handler wiring these are always the same value —
// the handler passes the JWT-derived staff ID — so the two never diverge
// in practice. The service does not enforce the equality, which means a
// future caller that supplies a different CreatedBy would record that
// value as the creator while the audit entry attributes the action to
// the context-derived staff user. The authorisation and the attribution
// would then be inconsistent.
//
// # Advisory name-uniqueness check
//
// The ExistsByName check at step 2 and the insert at step 4 are not
// atomic. Two concurrent Creates with the same name will both observe
// "not taken" and both attempt the insert; the outcome depends on whether
// the schema enforces a UNIQUE constraint on segments.name. If it does,
// the second insert fails at the database with a unique-violation error,
// wrapped with the prefix "create segment: " and served as a 500 by the
// handler — not the 409 that ErrSegmentNameTaken would have produced. If
// the schema does not enforce uniqueness, both inserts succeed and the
// table contains two segments sharing a name.
//
// # Creator ID is zeroed on staff-lookup failure
//
// The response's creator reference is built from a second staff lookup at
// step 6. As with GetByID, a lookup failure leaves Actor.ID at its zero
// value, discarding the correct creator ID held in seg.CreatedBy. See
// GetByID for the full analysis.
//
// # Errors
//
// Returns ErrStaffNotFoundOrInactive for the context-identity check,
// ErrSegmentNameTaken for a name collision, and a wrapped error for any
// repository failure (prefixes "check name existence: " and "create
// segment: "). The wrapped errors are not sentinels and are served
// through the handler's default 500 branch.
//
// # Concurrency
//
// Safe for concurrent use. See the advisory-check note above for the
// concurrency implications of the name check.
func (s *SegmentService) Create(ctx context.Context, params CreateParams) (*Segment, error) {
	actorID, ok := audit.GetStaffID(ctx)
	if !ok {
		return nil, ErrStaffNotFoundOrInactive
	}
	_, err := s.staffRepo.FindByID(ctx, actorID)
	if err != nil {
		return nil, ErrStaffNotFoundOrInactive
	}

	exists, err := s.segmentRepo.ExistsByName(ctx, params.Name)
	if err != nil {
		return nil, fmt.Errorf("check name existence: %w", err)
	}
	if exists {
		return nil, ErrSegmentNameTaken
	}

	var desc pgtype.Text
	if params.Description != nil {
		desc = pgtype.Text{String: *params.Description, Valid: true}
	}

	seg, err := s.segmentRepo.Create(ctx, sqlc.CreateSegmentParams{
		Name:        params.Name,
		Description: desc,
		CreatedBy:   params.CreatedBy,
	})
	if err != nil {
		return nil, fmt.Errorf("create segment: %w", err)
	}

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_SEGMENT_CREATE,
		EntityType: audit.ENTITY_TYPE_SEGMENT,
		EntityName: seg.Name,
		EntityID:   seg.ID,
		After:      seg,
	}); errLog != nil {
		log.Printf(
			"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
			audit.ACTION_SEGMENT_CREATE,
			audit.ENTITY_TYPE_SEGMENT,
			seg.ID,
			seg.Name,
			errLog,
		)
	}

	actor, err := s.staffRepo.FindByID(ctx, seg.CreatedBy)
	actorName := ""
	if err == nil {
		actorName = actor.Name
	}

	return &Segment{
		ID:          seg.ID,
		Name:        seg.Name,
		Description: seg.Description.String,
		CreatedBy: Actor{
			ID:   actor.ID,
			Name: actorName,
		},
		CreatedAt: seg.CreatedAt.Time,
		UpdatedAt: seg.UpdatedAt.Time,
	}, nil
}

// UpdateParams holds input for updating a segment.
//
// It is the service-layer counterpart of UpdateSegmentRequest. Both the
// name and description fields are pointers so that the handler can
// distinguish "field omitted" (nil) from "field set" (non-nil). The
// service applies only the fields whose pointers are non-nil; the rest
// retain their current values.
type UpdateParams struct {
	// ID is the internal segments.id being updated.
	ID int64

	// Name, when non-nil, replaces the segment's name. The service checks
	// that the new name does not collide with an existing segment's
	// name (unless it matches the current name, in which case the check
	// is skipped); a collision produces ErrSegmentNameTaken. An empty
	// string is accepted as a valid replacement by the service.
	Name *string

	// Description, when non-nil, replaces the segment's description. An
	// empty string produces an empty-string column value rather than
	// NULL; nil means "leave the existing description unchanged".
	Description *string
}

// Update updates an existing segment. Returns the updated domain Segment.
//
// # Flow
//
//  1. Verify the acting staff identity from the request context. A
//     missing identity produces ErrStaffNotFoundOrInactive.
//  2. Load the existing segment. A missing row returns ErrSegmentNotFound;
//     any other repository failure is wrapped with "get segment for update: "
//     and returned as a non-sentinel error.
//  3. Build an update struct pre-populated with the existing name and
//     description, so that fields not supplied by the caller retain
//     their current state.
//  4. If params.Name is non-nil AND differs from the existing name,
//     check for a name collision via ExistsByName. A collision produces
//     ErrSegmentNameTaken; otherwise the new name is applied to the
//     update struct. A params.Name equal to the existing name is treated
//     as a no-op — the existence check is skipped, and the unchanged
//     value is left in place.
//  5. If params.Description is non-nil, apply it to the update struct.
//  6. Issue the UPDATE.
//  7. Re-fetch the segment to obtain the post-update state.
//  8. Write an audit entry recording the before and after snapshots. An
//     audit failure is logged and does not abort the operation.
//  9. Look up the creating staff user's display name for the response.
//  10. Return the domain Segment.
//
// # No-op updates are not short-circuited
//
// Unlike the profile feature's UpdateProfile, this method does not check
// whether any field actually changed before issuing the UPDATE. A PATCH
// that supplies only the existing values will still write the row (updating
// its updated_at timestamp), still write an audit entry recording the
// before/after pair, and still perform the full chain of read-update-
// read-write-audit-lookup. The audit entry's Before and After snapshots
// will be identical, which is misleading in an audit trail.
//
// # Stale re-fetch on failure to read back
//
// The post-update re-fetch at step 7 is required for the response and for
// the audit entry's After snapshot. If it fails, the method returns a
// wrapped error ("fetch updated segment: "), and the handler serves it as
// a 500 — even though the write at step 6 has already landed. A client
// that treats the 500 as "the update failed" will retry against a row that
// already reflects the requested change.
//
// # Creator ID is zeroed on staff-lookup failure
//
// As with GetByID and Create, the response's creator reference is built
// from a staff lookup whose failure is silently absorbed. See GetByID for
// the analysis.
//
// # Advisory name-uniqueness check
//
// The ExistsByName check at step 4 and the UPDATE at step 6 are not
// atomic. Two concurrent Updates claiming the same new name — or a
// concurrent Update and Create — can both pass the check; the outcome
// depends on whether the schema enforces a UNIQUE constraint, the same
// as in Create.
//
// # Errors
//
// Returns ErrStaffNotFoundOrInactive, ErrSegmentNameTaken, or
// ErrSegmentNotFound (only when the existing segment row is missing).
// Other lookup, uniqueness-check, update, and re-fetch failures are
// wrapped, not collapsed to a not-found sentinel; the handlers map those
// to 500.
//
// # Concurrency
//
// Safe for concurrent use, but two concurrent Updates on the same segment
// are not serialised. The final state reflects whichever update commits
// last.
func (s *SegmentService) Update(ctx context.Context, params UpdateParams) (*Segment, error) {
	actorID, ok := audit.GetStaffID(ctx)
	if !ok {
		return nil, ErrStaffNotFoundOrInactive
	}
	_, err := s.staffRepo.FindByID(ctx, actorID)
	if err != nil {
		return nil, ErrStaffNotFoundOrInactive
	}

	existing, err := s.segmentRepo.FindByID(ctx, params.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSegmentNotFound
		}
		return nil, fmt.Errorf("get segment for update: %w", err)
	}

	update := sqlc.UpdateSegmentParams{
		ID:          params.ID,
		Name:        existing.Name,
		Description: existing.Description,
	}

	if params.Name != nil {
		if *params.Name != existing.Name {
			exists, err := s.segmentRepo.ExistsByName(ctx, *params.Name)
			if err != nil {
				return nil, fmt.Errorf("check name: %w", err)
			}
			if exists {
				return nil, ErrSegmentNameTaken
			}
			update.Name = *params.Name
		}
	}
	if params.Description != nil {
		update.Description = pgtype.Text{String: *params.Description, Valid: true}
	}

	if err := s.segmentRepo.Update(ctx, update); err != nil {
		return nil, fmt.Errorf("update segment: %w", err)
	}

	updated, err := s.segmentRepo.FindByID(ctx, params.ID)
	if err != nil {
		return nil, fmt.Errorf("fetch updated segment: %w", err)
	}

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_SEGMENT_UPDATE,
		EntityType: audit.ENTITY_TYPE_SEGMENT,
		EntityName: updated.Name,
		EntityID:   updated.ID,
		Before:     existing,
		After:      updated,
	}); errLog != nil {
		log.Printf(
			"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
			audit.ACTION_SEGMENT_UPDATE,
			audit.ENTITY_TYPE_SEGMENT,
			updated.ID,
			updated.Name,
			errLog,
		)
	}

	actor, err := s.staffRepo.FindByID(ctx, updated.CreatedBy)
	actorName := ""
	if err == nil {
		actorName = actor.Name
	}

	return &Segment{
		ID:          updated.ID,
		Name:        updated.Name,
		Description: updated.Description.String,
		CreatedBy: Actor{
			ID:   actor.ID,
			Name: actorName,
		},
		CreatedAt: updated.CreatedAt.Time,
		UpdatedAt: updated.UpdatedAt.Time,
	}, nil
}

// Delete deletes a segment only if it has no members.
//
// # Flow
//
//  1. Verify the acting staff identity from the request context. A
//     missing identity produces ErrStaffNotFoundOrInactive.
//  2. Load the existing segment. A failure collapses to
//     ErrSegmentNotFound.
//  3. Count the segment's members. A count failure is wrapped with
//     "check members: " and returned.
//  4. If the count is greater than zero, return ErrSegmentHasMembers.
//  5. Delete the segment row.
//  6. Write an audit entry recording the deletion. An audit failure is
//     logged and does not abort.
//
// # The member-count guard is not atomic
//
// The count at step 3 and the delete at step 5 are two separate database
// operations with no locking between them. A concurrent AddMember that
// commits between the two will succeed against a segment that is about
// to be deleted; the membership row's foreign key will either prevent
// the delete (producing a wrapped error the handler serves as 500) or
// be orphaned, depending on the schema's referential integrity
// configuration. The guard is therefore an optimistic check, not a
// guarantee; the authoritative protection is whatever the schema
// enforces on segment_members' foreign key to segments.
//
// # The delete is not cascaded by the service
//
// The service deletes only the segment row itself. If the schema declares
// ON DELETE CASCADE on segment_members' segment_id, the member rows are
// removed at the database level as a side effect — but since the guard at
// step 4 requires zero members, this path is not exercised in practice.
// If the schema does not declare a cascade and a race produces a member
// between steps 3 and 5, the delete will fail with a foreign-key
// violation, which is wrapped and served as a 500.
//
// # Errors
//
// Returns ErrStaffNotFoundOrInactive, ErrSegmentNotFound, or
// ErrSegmentHasMembers for the corresponding conditions. Returns wrapped
// errors with prefixes "check members: " or "delete segment: " for
// repository failures. None of the wrapped errors is a sentinel.
//
// # Concurrency
//
// Not serialised against concurrent member additions. See the atomicity
// note above.
func (s *SegmentService) Delete(ctx context.Context, id int64) error {
	actorID, ok := audit.GetStaffID(ctx)
	if !ok {
		return ErrStaffNotFoundOrInactive
	}
	_, err := s.staffRepo.FindByID(ctx, actorID)
	if err != nil {
		return ErrStaffNotFoundOrInactive
	}

	existing, err := s.segmentRepo.FindByID(ctx, id)
	if err != nil {
		return ErrSegmentNotFound
	}

	count, err := s.memberRepo.CountBySegment(ctx, id)
	if err != nil {
		return fmt.Errorf("check members: %w", err)
	}
	if count > 0 {
		return ErrSegmentHasMembers
	}

	if err := s.segmentRepo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete segment: %w", err)
	}

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_SEGMENT_DELETE,
		EntityType: audit.ENTITY_TYPE_SEGMENT,
		EntityName: existing.Name,
		EntityID:   existing.ID,
		Before:     existing,
	}); errLog != nil {
		log.Printf(
			"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
			audit.ACTION_SEGMENT_DELETE,
			audit.ENTITY_TYPE_SEGMENT,
			existing.ID,
			existing.Name,
			errLog,
		)
	}

	return nil
}
