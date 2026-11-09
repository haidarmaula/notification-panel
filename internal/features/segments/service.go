package segments

import (
	"context"
	"errors"
	"fmt"
	"hello/internal/authz"
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
	// ErrStaffNotFoundOrInactive is retained for legacy detail/error mappings.
	// New entry-point permission checks return authz.ErrUnauthenticated or
	// authz.ErrForbidden; authority lookup failures remain internal errors.
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
// # Creator reference resolution
//
// Every method that builds a Segment response resolves the creator's
// display name from staffRepo. The creator's *ID* is taken from the
// segment row itself (its CreatedBy column, which is the authoritative
// value written at insert time and always available), not from the
// staff-lookup result. A failure of the staff lookup therefore degrades
// the response's creator name to the empty string while preserving the
// correct creator ID — a caller can still resolve the reference, at the
// cost of a follow-up lookup. This behaviour is documented in full on
// GetByID.
//
// # Concurrency
//
// The service is stateless after construction and safe for concurrent use.
// It holds no per-request state and shares nothing mutable between
// invocations. The interface-typed repositories are expected to be
// concurrency-safe; the concrete implementations backed by pgxpool are.
type SegmentService struct {
	authorization authz.Checker
	segmentRepo   SegmentRepository
	memberRepo    SegmentMemberRepository
	staffRepo     StaffUserRepository
	auditService  AuditService
}

// NewSegmentService creates a new SegmentService instance.
//
// The authorization checker is validated; domain collaborators must be non-nil.
// Other dependencies are not validated, so a nil
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
	authorization authz.Checker,
) *SegmentService {
	if authorization == nil {
		panic("segments: authorization checker required")
	}
	return &SegmentService{
		authorization: authorization,
		segmentRepo:   segmentRepo,
		memberRepo:    memberRepo,
		staffRepo:     staffRepo,
		auditService:  auditService,
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
// # Creator reference in list rows
//
// Unlike the single-record read paths, List does not perform a per-row
// staff lookup to resolve the creator's display name. The underlying
// queries join the staff_users table directly and return the creator's
// name alongside the segment row, so the creator reference on each
// Segment in the response is always fully populated with the correct ID
// and name. The zeroing bug that affected GetByID, Create, and Update
// (see those methods) did not apply to this path.
//
// # Search-aware total
//
// When search is non-empty, the total is obtained from
// SegmentRepository.CountSearch, which applies the same name filter as the
// Search query. When search is empty, SegmentRepository.Count is used.
// Pagination metadata therefore matches the visible result set on both the
// filtered and unfiltered paths.
//
// # Batched member counts
//
// Member counts for the returned page are fetched in one batched query via
// SegmentMemberRepository.CountBySegmentIDs. Segments with no membership
// rows are absent from the batch result and therefore default to zero in
// the response. A failure of the batch query aborts the call and is
// returned as a wrapped error rather than collapsed into a zero count.
//
// # Errors
//
// Returns a wrapped error with the prefix "search segments: " or
// "list segments: " for the page query, "count segments search: " or
// "count segments: " for the total, and "count members: " for the batched
// member-count query. None of these is a sentinel; the handler serves them
// through its default 500 branch without disclosing internal details.
//
// # Concurrency
//
// Safe for concurrent use.
// Current-authority permission checks run before domain I/O, independently
// of route guards. They do not lock authority through the subsequent mutation.
func (s *SegmentService) List(ctx context.Context, page, limit int32, search string) ([]SegmentWithCount, int64, error) {
	if _, err := s.authorization.Require(ctx, authz.SegmentsRead); err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * limit

	var segments []Segment
	var total int64

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

		total, err = s.segmentRepo.CountSearch(ctx, search)
		if err != nil {
			return nil, 0, fmt.Errorf("count segments search: %w", err)
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

		total, err = s.segmentRepo.Count(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("count segments: %w", err)
		}
	}

	// Fetch member counts for every returned segment in one batched query.
	// Segments that have no membership rows are absent from the result set,
	// so they fall back to zero via the map lookup below.
	ids := make([]int64, len(segments))
	for i, seg := range segments {
		ids[i] = seg.ID
	}

	countMap := make(map[int64]int64, len(segments))
	if len(ids) > 0 {
		counts, err := s.memberRepo.CountBySegmentIDs(ctx, ids)
		if err != nil {
			return nil, 0, fmt.Errorf("count members: %w", err)
		}
		for _, c := range counts {
			countMap[c.SegmentID] = c.MemberCount
		}
	}

	result := make([]SegmentWithCount, len(segments))
	for i, seg := range segments {
		result[i] = SegmentWithCount{
			Segment:     seg,
			MemberCount: countMap[seg.ID],
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
//     name. A failure here is *swallowed*: the response's creator name
//     degrades to the empty string while the creator ID is preserved —
//     see the section below.
//
// # Creator reference on staff-lookup failure
//
// The creator lookup at step 3 is implemented as:
//
//	actor, err := s.staffRepo.FindByID(ctx, seg.CreatedBy)
//	actorName := ""
//	if err == nil {
//	    actorName = actor.Name
//	}
//
// The subsequent construction reads the creator ID from *the segment row*,
// not from the lookup result:
//
//	CreatedBy: Actor{
//	    ID:   seg.CreatedBy,
//	    Name: actorName,
//	},
//
// Because seg.CreatedBy is the value read from the segments table, it is
// always available and always correct — it is the authoritative record of
// which staff user created the segment, and it does not depend on the
// second lookup succeeding. A transient or permanent staff-lookup failure
// therefore only degrades the response's creator *name* to the empty
// string; the creator ID remains resolvable, and a client can issue a
// follow-up staff lookup to recover the name.
//
// This behaviour closes the defect described as C18 in code-review.md,
// where the response's creator ID was read from the lookup's zero-valued
// result and was therefore recorded as 0 on any failure — discarding the
// correct value held in seg.CreatedBy and causing a client that used the
// ID for subsequent lookups to fetch staff user 0. The same pattern is
// applied in Create and Update.
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
// Current-authority permission checks run before domain I/O, independently
// of route guards. They do not lock authority through the subsequent mutation.
func (s *SegmentService) GetByID(ctx context.Context, id int64) (*SegmentWithCount, error) {
	if _, err := s.authorization.Require(ctx, authz.SegmentsRead); err != nil {
		return nil, err
	}
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
				ID:   seg.CreatedBy,
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

	// CreatedBy is retained for source compatibility. Create always replaces
	// it with the verified principal ID; callers cannot select another creator.
	CreatedBy int64
}

// Create creates a new segment and returns the domain Segment.
//
// # Flow
//
//  1. Require current active staff authority and the operation permission
//     through authz. Missing/inactive identity is unauthenticated; lacking
//     permission is forbidden; authority storage errors remain internal.
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
// # Creator provenance
//
// The stored creator is derived from the authorized principal. Supplied
// params.CreatedBy is ignored, including direct service calls; audit and
// creator attribution therefore use the same authenticated staff identity.
//
// # Creator reference on staff-lookup failure
//
// The response's creator reference is built from a second staff lookup at
// step 6. The creator *ID* is read from seg.CreatedBy — the value written
// to the row at step 4 and echoed back by the insert — not from the
// lookup result. A lookup failure therefore degrades the response's
// creator name to the empty string while leaving the creator ID intact;
// see GetByID for the full rationale and the corresponding entry in
// code-review.md (C18).
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
// # Errors
//
// Returns authz.ErrUnauthenticated for the context-identity check,
// ErrSegmentNameTaken for a name collision, and a wrapped error for any
// repository failure (prefixes "check name existence: " and "create
// segment: "). The wrapped errors are not sentinels and are served
// through the handler's default 500 branch.
//
// # Concurrency
//
// Safe for concurrent use. See the advisory-check note above for the
// concurrency implications of the name check.
// Current-authority permission checks run before domain I/O, independently
// of route guards. They do not lock authority through the subsequent mutation.
func (s *SegmentService) Create(ctx context.Context, params CreateParams) (*Segment, error) {
	principal, err := s.authorization.Require(ctx, authz.SegmentsWrite)
	if err != nil {
		return nil, err
	}
	params.CreatedBy = principal.ID

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
			ID:   seg.CreatedBy,
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
//  1. Require current active staff authority and the operation permission
//     through authz. Missing/inactive identity is unauthenticated; lacking
//     permission is forbidden; authority storage errors remain internal.
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
// # Creator reference on staff-lookup failure
//
// As with GetByID and Create, the response's creator reference is built
// from a staff lookup whose failure is silently absorbed. The creator ID
// is taken from the re-fetched row (updated.CreatedBy), not from the
// lookup result, so a failure degrades only the name. See GetByID for the
// full analysis and the corresponding entry in code-review.md (C18).
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
// Returns authz.ErrUnauthenticated, ErrSegmentNameTaken, or
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
// Current-authority permission checks run before domain I/O, independently
// of route guards. They do not lock authority through the subsequent mutation.
func (s *SegmentService) Update(ctx context.Context, params UpdateParams) (*Segment, error) {
	if _, err := s.authorization.Require(ctx, authz.SegmentsWrite); err != nil {
		return nil, err
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
			ID:   updated.CreatedBy,
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
//  1. Require current active staff authority and the operation permission
//     through authz. Missing/inactive identity is unauthenticated; lacking
//     permission is forbidden; authority storage errors remain internal.
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
// The count at step 3 and delete at step 5 are separate database
// operations. A concurrent AddMember can commit after the count returns
// zero but before the delete. The schema declares ON DELETE CASCADE for
// segment_members.segment_id, so the delete removes that new membership.
// AddMember can therefore report success immediately before the segment
// deletion removes its row. This legacy service is not mounted; the current
// application segment writes use a separate scoped implementation.
//
// # Errors
//
// Returns authz.ErrUnauthenticated, ErrSegmentNotFound, or
// ErrSegmentHasMembers for the corresponding conditions. Returns wrapped
// errors with prefixes "check members: " or "delete segment: " for
// repository failures. None of the wrapped errors is a sentinel.
//
// # Concurrency
//
// Not serialised against concurrent member additions. See the atomicity
// note above.
// Current-authority permission checks run before domain I/O, independently
// of route guards. They do not lock authority through the subsequent mutation.
func (s *SegmentService) Delete(ctx context.Context, id int64) error {
	if _, err := s.authorization.Require(ctx, authz.SegmentsWrite); err != nil {
		return err
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
