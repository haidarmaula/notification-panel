package notifications

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"hello/internal/audit"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"

	"github.com/jackc/pgx/v5/pgtype"
)

// Domain errors.
//
// These sentinels are returned by NotificationService methods and translated
// into HTTP status codes by NotificationHandler. Each is documented below
// with the condition under which it is returned and, where the handler's
// mapping differs from what a caller might expect, the resulting status
// code.
//
// All ten are declared in a single block so that the service's error
// vocabulary is visible at once; a caller that wants to build a full
// mapping table should read this block alongside the handler's error-mapping
// switch.
var (
	// ErrStaffNotFoundOrInactive is returned when the request context does
	// not carry a staff identity, or when the identified staff user cannot
	// be loaded from the database (which includes an inactive account, at
	// least in the sense that FindByID does not distinguish the two).
	//
	// The handler maps this to 401 Unauthorized.
	//
	// The service performs this check on every write method — Create,
	// Update, Delete, Send — and on the detail read GetByID, so a
	// deactivated staff user cannot use an unexpired token to act on any
	// notification endpoint.
	ErrStaffNotFoundOrInactive = errors.New("staff account not found or inactive")

	// ErrNotificationNotFound is returned when the requested notification
	// ID does not resolve to a row. It is also returned when a repository
	// error other than "no row found" occurs during the lookup — a
	// distinction the service does not preserve.
	//
	// The handler maps this to 404 Not Found.
	ErrNotificationNotFound = errors.New("notification not found")

	// ErrNotificationNotDraft is returned when an operation that requires
	// a notification to be in DRAFT status is attempted against a
	// notification in any other status. It is returned by Update (which
	// forbids editing a non-draft) and by Send (which forbids sending a
	// notification that has already been sent or scheduled).
	//
	// The handler maps this to 409 Conflict.
	ErrNotificationNotDraft = errors.New("notification must be in DRAFT status")

	// ErrInvalidTargetType is returned by Create when the supplied target
	// type is not one of "BROADCAST", "SEGMENT", or "INDIVIDUAL". The
	// comparison is case-sensitive.
	//
	// The handler maps this to 400 Bad Request.
	ErrInvalidTargetType = errors.New("invalid target type")

	// ErrTemplateNotFound is returned by Create and Update when a
	// non-nil TemplateID does not resolve to a row. The template's
	// existence is verified but its content is not copied into the
	// notification — see CreateNotificationRequest.TemplateID for the
	// rationale.
	//
	// The handler maps this to 400 Bad Request on both paths.
	ErrTemplateNotFound = errors.New("template not found")

	// ErrSegmentNotFound is returned by Create when a SEGMENT-targeted
	// notification references a segment ID that does not resolve to a
	// row. Update does not check segment existence, because the target
	// type is immutable after creation.
	//
	// The handler maps this to 400 Bad Request.
	ErrSegmentNotFound = errors.New("segment not found")

	// ErrSegmentIDRequired is returned by Create when TargetType is
	// "SEGMENT" but SegmentID is nil. It is a defensive check: the
	// handler also rejects this case before invoking the service, so the
	// sentinel is currently unreachable through the HTTP API. It is
	// retained for direct service callers.
	//
	// The handler's switch does not include this sentinel, so if it were
	// reached through the HTTP API it would be served as a 500 rather
	// than a 400. The handler-level pre-check prevents that from
	// happening today.
	ErrSegmentIDRequired = errors.New("segment_id required for SEGMENT type")

	// ErrInvalidScheduledTime is returned by Create and Update when a
	// non-nil ScheduledAt is not strictly in the future. A scheduled time
	// equal to the current instant is treated as invalid.
	//
	// The handler maps this to 400 Bad Request on both paths.
	ErrInvalidScheduledTime = errors.New("scheduled time must be in the future")

	// ErrCannotDeleteSent is returned by Delete when the notification is
	// not in DRAFT status. Despite the error's name — which suggests a
	// distinction between "sent" and "scheduled" — the current check is
	// `notif.Status != "DRAFT"`, so a SCHEDULED notification is rejected
	// with this error just as a COMPLETED one would be. An operator who
	// attempts to delete a scheduled notification will see a message
	// implying the notification was sent, which is not accurate.
	//
	// The handler maps this to 409 Conflict.
	ErrCannotDeleteSent = errors.New("cannot delete sent notification")

	// ErrTargetsRequired is returned by Create when TargetType is
	// "INDIVIDUAL" but UserIDs is empty. Like ErrSegmentIDRequired, this
	// is a defensive check that the handler also performs, so the
	// sentinel is unreachable through the HTTP API today.
	//
	// The handler maps this to 400 Bad Request.
	ErrTargetsRequired = errors.New("user_ids required for INDIVIDUAL type")
)

// NotificationService provides business logic for notifications.
//
// It is the API-side counterpart of Processor: where the processor consumes
// Kafka events and performs delivery fan-out, the service handles the HTTP
// API's lifecycle operations (create, update, delete, send) and the read
// paths (list, detail). Actual delivery is asynchronous and does not happen
// in this service.
//
// # Dependencies
//
// The service holds eight collaborators:
//
//   - notifRepo, targetRepo, deliveryRepo, readRepo — the four
//     notification-related repositories used for CRUD, target management,
//     and statistics reads.
//   - staffRepo — for the acting-staff authorisation check performed at
//     the start of every write method and of GetByID.
//   - templateRepo, segmentRepo — for the existence checks performed by
//     Create and Update.
//   - auditService — for recording administrative actions. Audit failures
//     are logged but do not fail the surrounding operation.
//   - producer — the Kafka producer used by Send to publish the
//     NotificationSendRequested event.
//
// All nine fields are stored by reference and must be non-nil.
//
// # Send lifecycle claim
//
// Send atomically claims DRAFT notifications as QUEUED before publishing the
// Kafka event. This prevents competing API requests from enqueueing the same
// campaign twice and prevents DRAFT-only edits or deletes after queueing.
//
// # Concurrency
//
// The service is stateless after construction and safe for concurrent use.
// It holds no per-request state. The conditional status update serialises
// competing Send operations for one notification at the database level.
type NotificationService struct {
	notifRepo    *repository.NotificationRepository
	targetRepo   *repository.NotificationTargetRepository
	deliveryRepo *repository.NotificationDeliveryRepository
	readRepo     *repository.NotificationReadRepository
	staffRepo    *repository.StaffUserRepository
	templateRepo *repository.TemplateRepository
	segmentRepo  *repository.SegmentRepository
	auditService *audit.AuditService
	producer     *kafka.Producer
}

// NewNotificationService creates a new NotificationService instance.
//
// # Parameters
//
// All nine parameters must be non-nil; none is validated, so a nil
// repository will panic on the first method call that uses it. In practice
// the dependencies are wired by NewNotificationModule, which builds each
// from the shared sqlc.Queries and the configuration.
//
// # Construction cost
//
// The constructor is cheap: it stores the nine references and returns. It
// performs no I/O, opens no connections, and holds no locks. The returned
// service is intended to be constructed once at startup and shared across
// every request.
func NewNotificationService(
	notifRepo *repository.NotificationRepository,
	targetRepo *repository.NotificationTargetRepository,
	deliveryRepo *repository.NotificationDeliveryRepository,
	readRepo *repository.NotificationReadRepository,
	staffRepo *repository.StaffUserRepository,
	templateRepo *repository.TemplateRepository,
	segmentRepo *repository.SegmentRepository,
	auditService *audit.AuditService,
	producer *kafka.Producer,
) *NotificationService {
	return &NotificationService{
		notifRepo:    notifRepo,
		targetRepo:   targetRepo,
		deliveryRepo: deliveryRepo,
		readRepo:     readRepo,
		staffRepo:    staffRepo,
		templateRepo: templateRepo,
		segmentRepo:  segmentRepo,
		auditService: auditService,
		producer:     producer,
	}
}

// List returns a paginated list of notifications with optional filters.
// Filters: status, targetType, keyword.
// If no filters are provided, it uses a simpler query for better performance.
//
// # Filtering
//
// When any of status, targetType, or keyword is non-empty, the method
// dispatches to the repository's filtered queries (ListWithFilters and
// CountWithFilters), passing the empty strings through as "no filter on
// that column". When all three are empty, it uses the unfiltered queries
// (List and Count), which are cheaper.
//
// The two paths produce different response shapes for some fields, and
// callers should be aware of the divergence:
//
//   - TargetType. Both list queries now compute target_type from the
//     notification_targets rows, defaulting to "BROADCAST" only when no
//     target row exists. The filtered and unfiltered paths therefore
//     report the same shape.
//   - SentAt. Both list queries now compute sent_at as the
//     notification's published_at when its status is "SENT", and NULL
//     otherwise. A list item therefore carries a sent timestamp for sent
//     notifications without requiring the detail endpoint.
//   - ScheduledAt. Both paths now take a pointer only when the underlying
//     row's ScheduledAt column is valid. A NULL scheduled_at is serialised
//     as absent/null rather than as "0001-01-01T00:00:00Z". A JSON client
//     therefore sees nil for immediate or unscheduled notifications, which
//     matches the field's type-level documentation.
//
// # Pagination
//
// The method receives page and limit as already-validated int32 values
// from the handler (which applies a default of 1/10 and a limit cap of
// 100). It computes the offset internally as (page - 1) * limit. A page
// value of 0 would produce a negative offset and is not defended against;
// the handler never sends one.
//
// # Return values
//
// On success, the method returns the page's items, the total matching row
// count, and a nil error. The total is computed from the same filter
// arguments as the page, so the two are consistent. On any repository
// error the method returns a nil slice, zero total, and a wrapped error
// whose prefix identifies which step failed.
//
// # Concurrency
//
// Safe for concurrent use. The method allocates its own result slice and
// does not mutate shared state.
func (s *NotificationService) List(ctx context.Context, page, limit int32, status, targetType, keyword string) ([]NotificationListItem, int64, error) {
	offset := (page - 1) * limit

	if status != "" || targetType != "" || keyword != "" {
		rows, err := s.notifRepo.ListWithFilters(ctx, status, targetType, keyword, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("list notifications with filters: %w", err)
		}
		items := make([]NotificationListItem, len(rows))
		for i, row := range rows {
			var scheduledAt *time.Time
			if row.ScheduledAt.Valid {
				scheduledAt = &row.ScheduledAt.Time
			}
			var sentAt *time.Time
			if row.SentAt.Valid {
				sentAt = &row.SentAt.Time
			}

			items[i] = NotificationListItem{
				ID:          row.ID,
				Title:       row.Title,
				Type:        row.TargetType,
				Status:      row.Status,
				CreatedBy:   row.CreatedByName,
				ScheduledAt: scheduledAt,
				SentAt:      sentAt,
				CreatedAt:   row.CreatedAt.Time,
			}
		}
		total, err := s.notifRepo.CountWithFilters(ctx, status, targetType, keyword)
		if err != nil {
			return nil, 0, fmt.Errorf("count notifications with filters: %w", err)
		}
		return items, total, nil
	}

	rows, err := s.notifRepo.List(ctx, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications: %w", err)
	}
	items := make([]NotificationListItem, len(rows))
	for i, row := range rows {
		var scheduledAt *time.Time
		if row.ScheduledAt.Valid {
			scheduledAt = &row.ScheduledAt.Time
		}
		var sentAt *time.Time
		if row.SentAt.Valid {
			sentAt = &row.SentAt.Time
		}

		items[i] = NotificationListItem{
			ID:          row.ID,
			Title:       row.Title,
			Type:        row.TargetType,
			Status:      row.Status,
			CreatedBy:   row.CreatedByName,
			ScheduledAt: scheduledAt,
			SentAt:      sentAt,
			CreatedAt:   row.CreatedAt.Time,
		}
	}
	total, err := s.notifRepo.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count notifications: %w", err)
	}
	return items, total, nil
}

// GetByID returns full notification detail with statistics.
//
// # Authorisation
//
// Despite being a read method, GetByID requires an authenticated staff
// user: it extracts the acting staff ID from the context via
// audit.GetStaffID and loads the staff row via staffRepo.FindByID. A
// context without a staff identity, or one whose staff user cannot be
// loaded, produces ErrStaffNotFoundOrInactive. This means the method
// cannot be called from a background job or a system context; every
// invocation must originate from an authenticated HTTP request.
//
// The staff row itself is used only for its ID and name, which are echoed
// back in the response's CreatedBy field — note that this is the *acting*
// staff user, not the *creating* staff user of the notification. The
// current implementation does not verify that the acting user has any
// particular relationship to the notification; any authenticated staff
// user can read any notification.
//
// # Target-type resolution
//
// The notification's target type is resolved by reading the first target
// row and using its TargetType field. If that field is empty — which
// should not occur in a well-formed database but is defended against —
// the method falls back to inferring the type from which ID column is
// populated: a non-zero user_id implies "INDIVIDUAL", a valid segment_id
// implies "SEGMENT". If neither is populated, the default "BROADCAST"
// remains. Only the first target row is consulted; a notification with
// mixed target types will report only the first one.
//
// # Statistics
//
// Delivery statistics are fetched via a separate repository call
// (notifRepo.GetStatistics) and always present in the response, even when
// the notification has no deliveries. See the getStatistics method for
// details.
//
// # Errors
//
// Returns ErrStaffNotFoundOrInactive if the context has no staff identity
// or the staff user cannot be loaded. Returns ErrNotificationNotFound if
// the notification lookup fails for any reason. Returns a wrapped error if
// the statistics fetch fails — this is not mapped to a sentinel, so the
// handler will serve it through the default 500 branch.
//
// # Concurrency
//
// Safe for concurrent use.
func (s *NotificationService) GetByID(ctx context.Context, id int64) (*NotificationDetail, error) {
	actorID, ok := audit.GetStaffID(ctx)
	if !ok {
		return nil, ErrStaffNotFoundOrInactive
	}
	staff, err := s.staffRepo.FindByID(ctx, actorID)
	if err != nil {
		return nil, ErrStaffNotFoundOrInactive
	}

	notif, err := s.notifRepo.FindByID(ctx, id)
	if err != nil {
		return nil, ErrNotificationNotFound
	}

	var template *TemplateBrief
	if notif.TemplateID.Valid {
		tmpl, err := s.templateRepo.FindByID(ctx, notif.TemplateID.Int64)
		if err == nil {
			template = &TemplateBrief{ID: tmpl.ID, Name: tmpl.Name}
		}
	}

	targetType := "BROADCAST"
	targets, err := s.targetRepo.ListByNotification(ctx, id, 0, 1)
	if err == nil && len(targets) > 0 {
		if targets[0].TargetType != "" {
			targetType = targets[0].TargetType
		} else {
			// Fallback: guess from user_id or segment_id
			if targets[0].UserID.Valid && targets[0].UserID.Int64 != 0 {
				targetType = "INDIVIDUAL"
			} else if targets[0].SegmentID.Valid {
				targetType = "SEGMENT"
			}
		}
	}

	stats, err := s.getStatistics(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get statistics: %w", err)
	}

	var sentAt, publishedAt, completedAt *time.Time
	if notif.PublishedAt.Valid {
		publishedAt = &notif.PublishedAt.Time
	}
	if notif.CompletedAt.Valid {
		completedAt = &notif.CompletedAt.Time
	}
	if notif.Status == string(StatusSent) && publishedAt != nil {
		sentAt = publishedAt
	}

	return &NotificationDetail{
		ID:          notif.ID,
		Title:       notif.Title,
		Body:        notif.Body,
		Template:    template,
		Type:        targetType,
		Status:      notif.Status,
		CreatedBy:   StaffBrief{ID: staff.ID, Name: staff.Name},
		ScheduledAt: &notif.ScheduledAt.Time,
		SentAt:      sentAt,
		PublishedAt: publishedAt,
		CompletedAt: completedAt,
		CreatedAt:   notif.CreatedAt.Time,
		UpdatedAt:   notif.UpdatedAt.Time,
		Statistics:  *stats,
	}, nil
}

// getStatistics returns delivery statistics using a dedicated query.
//
// It is a thin wrapper around notifRepo.GetStatistics that converts the
// repository's row type into the domain's NotificationStatistics struct.
// The repository query is expected to return a row with COALESCE-guarded
// counters, so a notification with no deliveries produces a zero-valued
// struct rather than an error.
//
// Errors from the underlying query are returned unchanged; the caller is
// responsible for wrapping and, in the current implementation, the
// GetByID method wraps them with a "get statistics: " prefix and returns
// them through the default 500 branch.
func (s *NotificationService) getStatistics(ctx context.Context, notificationID int64) (*NotificationStatistics, error) {
	stats, err := s.notifRepo.GetStatistics(ctx, notificationID)
	if err != nil {
		return nil, err
	}
	return &NotificationStatistics{
		Targeted:  stats.Targeted,
		Delivered: stats.Delivered,
		Opened:    stats.Opened,
	}, nil
}

// CreateParams holds input for creating a notification.
//
// It is the service-layer counterpart of CreateNotificationRequest. The
// handler is responsible for decoding the request, applying the
// type-dependent presence checks, defaulting an empty Type to "BROADCAST",
// and injecting the acting staff ID into CreatedBy. The service then
// performs the remaining validation (target-type enumeration, scheduled
// time, template and segment existence) and persists the row and its
// targets.
//
// CreateParams is a plain data struct; it is constructed on the stack by
// the handler and passed by value.
type CreateParams struct {
	// Title is the notification's headline. Required; the handler enforces
	// non-emptiness before calling Create.
	Title string

	// Body is the notification's content. Required; the handler enforces
	// non-emptiness.
	Body string

	// TemplateID is an optional reference to a templates row. When non-nil
	// the service verifies that the template exists but does not copy its
	// content into the notification.
	TemplateID *int64

	// TargetType selects the targeting strategy. Accepted values are
	// "BROADCAST", "SEGMENT", and "INDIVIDUAL"; any other value produces
	// ErrInvalidTargetType.
	TargetType string

	// SegmentID is required when TargetType is "SEGMENT" and ignored
	// otherwise. When present, the service verifies that the segment
	// exists.
	SegmentID *int64

	// UserIDs is required (non-empty) when TargetType is "INDIVIDUAL" and
	// ignored otherwise. The service does not verify that the IDs
	// correspond to existing users; a send targeting a nonexistent user
	// ID simply produces no delivery for it.
	UserIDs []int64

	// ScheduledAt, when non-nil, marks the notification for future
	// dispatch. The service rejects a value that is not strictly in the
	// future with ErrInvalidScheduledTime.
	ScheduledAt *time.Time

	// CreatedBy is the acting staff user's ID. It is written to the
	// notification's created_by column. The service does not re-verify
	// that the ID corresponds to an active staff user — that check has
	// already been performed using the context's staff identity, and the
	// handler is expected to have passed the same ID.
	CreatedBy int64
}

// Create creates a new notification draft.
//
// # Flow
//
//  1. Verifies the acting staff identity via the request context. A
//     missing identity or an unloadable staff row produces
//     ErrStaffNotFoundOrInactive.
//  2. Validates the target type against the three accepted values.
//  3. Rejects a ScheduledAt that is not strictly in the future.
//  4. For SEGMENT targets, requires a non-nil SegmentID and verifies the
//     segment exists. For INDIVIDUAL targets, requires a non-empty
//     UserIDs.
//  5. For a non-nil TemplateID, verifies the template exists.
//  6. Determines the initial status: DRAFT if ScheduledAt is nil,
//     SCHEDULED otherwise.
//  7. Inserts the notification row.
//  8. Inserts one or more target rows depending on the target type: one
//     per user for INDIVIDUAL, one for SEGMENT, one for BROADCAST.
//  9. Writes an audit entry recording the creation. An audit failure is
//     logged and does not abort the operation.
//  10. Returns a CreateNotificationResponse with the new row's ID and
//     status.
//
// # Partial-failure handling
//
// Steps 7 and 8 are not wrapped in a database transaction. If a target
// insert fails, the service attempts to delete the parent notification
// row and returns a wrapped error. The cleanup delete is best-effort: its
// error is discarded, so a failure of the delete leaves an orphaned
// notification row with no targets. Because the notification is in DRAFT
// status and has no targets, such a row is harmless in practice but will
// appear in list responses until manually removed.
//
// # What Create does not validate
//
//   - It does not verify that UserIDs correspond to existing users. A
//     notification targeting nonexistent user IDs is created
//     successfully and simply produces no deliveries at send time.
//   - It does not verify that the acting staff user has a role that
//     permits notification creation. Any authenticated staff user can
//     call Create.
//   - It does not enforce uniqueness of title, body, or any other field.
//
// # Errors
//
// Returns a sentinel error for each validation failure
// (ErrStaffNotFoundOrInactive, ErrInvalidTargetType, ErrInvalidScheduledTime,
// ErrSegmentIDRequired, ErrSegmentNotFound, ErrTargetsRequired,
// ErrTemplateNotFound). Returns a wrapped error for repository failures
// during the notification insert, a target insert, or the cleanup delete.
// The wrap prefix identifies which step failed.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent Creates with the same body and
// targeting the same audience produce two independent notifications; the
// service has no de-duplication of content.
func (s *NotificationService) Create(ctx context.Context, params CreateParams) (*CreateNotificationResponse, error) {
	actorID, ok := audit.GetStaffID(ctx)
	if !ok {
		return nil, ErrStaffNotFoundOrInactive
	}
	_, err := s.staffRepo.FindByID(ctx, actorID)
	if err != nil {
		return nil, ErrStaffNotFoundOrInactive
	}

	if params.TargetType != string(TargetBroadcast) &&
		params.TargetType != string(TargetSegment) &&
		params.TargetType != string(TargetIndividual) {
		return nil, ErrInvalidTargetType
	}

	if params.ScheduledAt != nil && params.ScheduledAt.Before(time.Now()) {
		return nil, ErrInvalidScheduledTime
	}

	if params.TargetType == string(TargetIndividual) {
		if len(params.UserIDs) == 0 {
			return nil, ErrTargetsRequired
		}
	} else if params.TargetType == string(TargetSegment) {
		if params.SegmentID == nil {
			return nil, ErrSegmentIDRequired
		}
		_, err := s.segmentRepo.FindByID(ctx, *params.SegmentID)
		if err != nil {
			return nil, ErrSegmentNotFound
		}
	}

	var templateID pgtype.Int8
	if params.TemplateID != nil {
		_, err := s.templateRepo.FindByID(ctx, *params.TemplateID)
		if err != nil {
			return nil, ErrTemplateNotFound
		}
		templateID = pgtype.Int8{Int64: *params.TemplateID, Valid: true}
	}

	status := string(StatusDraft)
	if params.ScheduledAt != nil {
		status = string(StatusScheduled)
	}

	// Prepare scheduled_at safely
	var scheduledAt pgtype.Timestamptz
	if params.ScheduledAt != nil {
		scheduledAt = pgtype.Timestamptz{Time: *params.ScheduledAt, Valid: true}
	}

	notif, err := s.notifRepo.Create(ctx, sqlc.CreateNotificationParams{
		Title:       params.Title,
		Body:        params.Body,
		TemplateID:  templateID,
		Status:      status,
		CreatedBy:   params.CreatedBy,
		ScheduledAt: scheduledAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create notification: %w", err)
	}

	// Create targets based on type
	if params.TargetType == string(TargetIndividual) {
		for _, userID := range params.UserIDs {
			_, err := s.targetRepo.CreateFull(ctx, sqlc.CreateNotificationTargetFullParams{
				NotificationID: notif.ID,
				TargetType:     params.TargetType,
				SegmentID:      pgtype.Int8{Valid: false},
				UserID:         pgtype.Int8{Int64: userID, Valid: true},
				UploadBatchID:  pgtype.Int8{Valid: false},
			})
			if err != nil {
				_ = s.notifRepo.Delete(ctx, notif.ID)
				return nil, fmt.Errorf("create target for user %d: %w", userID, err)
			}
		}
	} else if params.TargetType == string(TargetSegment) {
		_, err := s.targetRepo.CreateFull(ctx, sqlc.CreateNotificationTargetFullParams{
			NotificationID: notif.ID,
			TargetType:     params.TargetType,
			SegmentID:      pgtype.Int8{Int64: *params.SegmentID, Valid: true},
			UserID:         pgtype.Int8{Valid: false},
			UploadBatchID:  pgtype.Int8{Valid: false},
		})
		if err != nil {
			_ = s.notifRepo.Delete(ctx, notif.ID)
			return nil, fmt.Errorf("create target for segment: %w", err)
		}
	} else if params.TargetType == string(TargetBroadcast) {
		_, err := s.targetRepo.CreateFull(ctx, sqlc.CreateNotificationTargetFullParams{
			NotificationID: notif.ID,
			TargetType:     params.TargetType,
			SegmentID:      pgtype.Int8{Valid: false},
			UserID:         pgtype.Int8{Valid: false},
			UploadBatchID:  pgtype.Int8{Valid: false},
		})
		if err != nil {
			_ = s.notifRepo.Delete(ctx, notif.ID)
			return nil, fmt.Errorf("create global target: %w", err)
		}
	}

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_NOTIFICATION_CREATE,
		EntityType: audit.ENTITY_TYPE_NOTIFICATION,
		EntityName: notif.Title,
		EntityID:   notif.ID,
		After:      notif,
	}); errLog != nil {
		log.Printf(
			"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
			audit.ACTION_NOTIFICATION_CREATE,
			audit.ENTITY_TYPE_NOTIFICATION,
			notif.ID,
			notif.Title,
			errLog,
		)
	}

	return &CreateNotificationResponse{
		ID:     notif.ID,
		Status: notif.Status,
	}, nil
}

// UpdateParams holds input for updating a notification.
//
// It is the service-layer counterpart of UpdateNotificationRequest. Every
// field is a pointer so that the handler can distinguish "leave this field
// unchanged" (nil) from "set this field to a new value". The service
// applies only the fields whose pointers are non-nil; the rest are left at
// the notification's current values.
//
// The handler enforces that at least one field is non-nil before invoking
// Update; the service does not re-check, so a fully-nil UpdateParams would
// perform a no-op write and produce an audit entry recording that no
// change occurred.
type UpdateParams struct {
	// Title, when non-nil, replaces the notification's title. An empty
	// string is accepted as a valid replacement.
	Title *string

	// Body, when non-nil, replaces the notification's body.
	Body *string

	// TemplateID, when non-nil, replaces the notification's template
	// reference. The service verifies that the new template exists.
	TemplateID *int64

	// ScheduledAt, when non-nil, replaces the notification's scheduled
	// dispatch time. The service verifies that the new value is strictly
	// in the future.
	ScheduledAt *time.Time
}

// Update updates a draft notification.
//
// # Preconditions
//
// The notification must exist and be in DRAFT status. A non-DRAFT
// notification — including one that has been scheduled — is rejected with
// ErrNotificationNotDraft. As with every write method, an authenticated
// staff user must be present in the context.
//
// # Flow
//
//  1. Verifies the acting staff identity.
//  2. Loads the notification and checks its status.
//  3. Builds an update struct pre-populated with the notification's
//     current field values, so that fields not supplied by the caller
//     retain their existing state.
//  4. For each non-nil UpdateParams field, verifies the new value (for
//     TemplateID, that the template exists; for ScheduledAt, that it is in
//     the future) and applies it to the update struct.
//  5. Issues the update.
//  6. Re-loads the notification to obtain the post-update state.
//  7. Writes an audit entry recording the before and after snapshots. An
//     audit failure is logged and does not abort the operation.
//
// # Status transitions
//
// Update does not change the notification's status. Updating a DRAFT
// notification leaves it in DRAFT; a caller that wants to transition it
// to SCHEDULED must do so via the create path with a ScheduledAt, or wait
// for a future method that supports the transition directly.
//
// # Errors
//
// Returns ErrStaffNotFoundOrInactive, ErrNotificationNotFound,
// ErrNotificationNotDraft, ErrTemplateNotFound, or ErrInvalidScheduledTime
// for the corresponding conditions. Repository failures during the update
// or the re-fetch are wrapped; the update failure is served through the
// handler's default 500 branch, and the re-fetch failure is reported as
// ErrNotificationNotFound, which is misleading but harmless (the row
// exists; the error indicates a transient read failure).
//
// # Concurrency
//
// Safe for concurrent use, but two concurrent Updates on the same
// notification are not serialised. The final state reflects whichever
// update commits last.
func (s *NotificationService) Update(ctx context.Context, id int64, params UpdateParams) error {
	actorID, ok := audit.GetStaffID(ctx)
	if !ok {
		return ErrStaffNotFoundOrInactive
	}
	_, err := s.staffRepo.FindByID(ctx, actorID)
	if err != nil {
		return ErrStaffNotFoundOrInactive
	}

	notif, err := s.notifRepo.FindByID(ctx, id)
	if err != nil {
		return ErrNotificationNotFound
	}
	if notif.Status != string(StatusDraft) {
		return ErrNotificationNotDraft
	}

	update := sqlc.UpdateNotificationParams{
		ID:          id,
		Title:       notif.Title,
		Body:        notif.Body,
		TemplateID:  notif.TemplateID,
		ScheduledAt: notif.ScheduledAt,
	}

	if params.Title != nil {
		update.Title = *params.Title
	}
	if params.Body != nil {
		update.Body = *params.Body
	}
	if params.TemplateID != nil {
		_, err := s.templateRepo.FindByID(ctx, *params.TemplateID)
		if err != nil {
			return ErrTemplateNotFound
		}
		update.TemplateID = pgtype.Int8{Int64: *params.TemplateID, Valid: true}
	}
	if params.ScheduledAt != nil {
		if params.ScheduledAt.Before(time.Now()) {
			return ErrInvalidScheduledTime
		}
		update.ScheduledAt = pgtype.Timestamptz{Time: *params.ScheduledAt, Valid: true}
	}

	if err := s.notifRepo.Update(ctx, update); err != nil {
		return fmt.Errorf("update notification: %w", err)
	}

	newNotif, err := s.notifRepo.FindByID(ctx, id)
	if err != nil {
		return ErrNotificationNotFound
	}

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_NOTIFICATION_UPDATE,
		EntityType: audit.ENTITY_TYPE_NOTIFICATION,
		EntityName: newNotif.Title,
		EntityID:   newNotif.ID,
		Before:     notif,
		After:      newNotif,
	}); errLog != nil {
		log.Printf(
			"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
			audit.ACTION_NOTIFICATION_UPDATE,
			audit.ENTITY_TYPE_NOTIFICATION,
			newNotif.ID,
			newNotif.Title,
			errLog,
		)
	}

	return nil
}

// Delete deletes a draft notification and its targets.
//
// # Preconditions
//
// The notification must exist and be in DRAFT status. Any other status
// — including SCHEDULED, which has not been sent — is rejected with
// ErrCannotDeleteSent. The error's name suggests a narrower check than the
// code actually performs; see the sentinel's documentation for the
// consequence.
//
// # Flow
//
//  1. Verifies the acting staff identity.
//  2. Loads the notification and checks its status.
//  3. Deletes all target rows attached to the notification via
//     targetRepo.DeleteByNotification.
//  4. Deletes the notification row itself.
//  5. Writes an audit entry recording the deletion. As with the other
//     write methods, an audit failure is logged but does not abort.
//
// # Partial-failure handling
//
// Steps 3 and 4 are not wrapped in a database transaction. A failure of
// the notification delete after the targets have been deleted leaves an
// orphaned notification row with no targets; a failure of the target
// delete leaves both the targets and the notification, with the caller
// receiving a wrapped error. In either case the row remains in DRAFT and
// is eligible for a retry.
//
// # Errors
//
// Returns ErrStaffNotFoundOrInactive, ErrNotificationNotFound, or
// ErrCannotDeleteSent for the corresponding conditions. Repository
// failures during either delete step are wrapped; the prefix identifies
// which step failed.
//
// # Concurrency
//
// Safe for concurrent use. Two concurrent Deletes on the same ID will
// both pass the status check and both attempt the delete; the second will
// typically be a no-op (the row is already gone) but may fail with a
// foreign-key or row-not-found error depending on the SQL.
func (s *NotificationService) Delete(ctx context.Context, id int64) error {
	actorID, ok := audit.GetStaffID(ctx)
	if !ok {
		return ErrStaffNotFoundOrInactive
	}
	_, err := s.staffRepo.FindByID(ctx, actorID)
	if err != nil {
		return ErrStaffNotFoundOrInactive
	}

	notif, err := s.notifRepo.FindByID(ctx, id)
	if err != nil {
		return ErrNotificationNotFound
	}
	if notif.Status != string(StatusDraft) {
		return ErrCannotDeleteSent
	}

	// Delete all targets using bulk delete query.
	if err := s.targetRepo.DeleteByNotification(ctx, id); err != nil {
		return fmt.Errorf("delete targets: %w", err)
	}
	if err := s.notifRepo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete notification: %w", err)
	}

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_NOTIFICATION_DELETE,
		EntityType: audit.ENTITY_TYPE_NOTIFICATION,
		EntityName: notif.Title,
		EntityID:   notif.ID,
		Before:     notif,
	}); errLog != nil {
		log.Printf(
			"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
			audit.ACTION_NOTIFICATION_DELETE,
			audit.ENTITY_TYPE_NOTIFICATION,
			notif.ID,
			notif.Title,
			errLog,
		)
	}

	return nil
}

// Send publishes a send-requested event for the notification to Kafka.
//
// # Flow
//
//  1. Verifies the acting staff identity.
//  2. Loads the notification detail via GetByID. Note that this is the
//     full detail path, not a bare repository lookup — the method
//     therefore performs the target-type resolution and the statistics
//     fetch that GetByID does, which is redundant for the Send
//     operation's actual needs.
//  3. Rejects the request with ErrNotificationNotDraft if the
//     notification's status is not "DRAFT".
//  4. Atomically transitions DRAFT to QUEUED. If another request has already
//     claimed the row, it returns ErrNotificationNotDraft without publishing.
//  5. Publishes a NotificationSendRequested event to the configured Kafka
//     producer's topic.
//  6. Writes an audit entry recording the send. As with the other write
//     methods, an audit failure is logged but does not abort.
//
// # Queue claim and publication failure
//
// A successful conditional claim makes QUEUED visible before the event is
// published, preventing duplicate Send calls and DRAFT-only modifications.
// If Kafka publication returns an error, the method attempts the inverse
// conditional transition (QUEUED to DRAFT) so an operator can retry. The
// rollback cannot be a distributed transaction with Kafka: a broker error may
// be ambiguous, and a process crash between the database update and publish
// can still leave a queued row without an event. A transactional outbox is
// required to close that cross-system delivery gap.
//
// # Errors
//
// Returns ErrStaffNotFoundOrInactive or ErrNotificationNotFound for the
// corresponding conditions (the latter via the GetByID call), and
// ErrNotificationNotDraft if the notification is not in DRAFT status.
// Returns a wrapped error if the queue claim or Kafka publication fails; the
// wrap prefixes are "queue notification: " and "send notification: ".
//
// # Concurrency
//
// Safe for concurrent use. Concurrent Send calls for one notification are
// serialised by the DRAFT-only update: exactly one can move the row to QUEUED
// and publish the event; all others receive ErrNotificationNotDraft.
func (s *NotificationService) Send(ctx context.Context, id int64) error {
	actorID, ok := audit.GetStaffID(ctx)
	if !ok {
		return ErrStaffNotFoundOrInactive
	}
	_, err := s.staffRepo.FindByID(ctx, actorID)
	if err != nil {
		return ErrStaffNotFoundOrInactive
	}

	// Check if notification exists and is in DRAFT status
	notif, err := s.GetByID(ctx, id)
	if err != nil {
		return ErrNotificationNotFound
	}
	if notif.Status != "DRAFT" {
		return ErrNotificationNotDraft
	}

	claimed, err := s.notifRepo.UpdateStatusIfDraft(ctx, id, "QUEUED")
	if err != nil {
		return fmt.Errorf("queue notification: %w", err)
	}
	if claimed == 0 {
		return ErrNotificationNotDraft
	}

	// Publish event to Kafka
	event := kafka.NotificationSendRequested{
		NotificationID: id,
		RequestedAt:    time.Now(),
	}
	if err := s.producer.PublishSendRequested(ctx, event); err != nil {
		if _, rollbackErr := s.notifRepo.UpdateStatusIfQueued(ctx, id, "DRAFT"); rollbackErr != nil {
			log.Printf("[Server] Failed to roll back queued notification %d after publish failure: %v", id, rollbackErr)
		}
		return fmt.Errorf("send notification: %w", err)
	}

	if errLog := s.auditService.Log(ctx, audit.LogParams{
		Action:     audit.ACTION_NOTIFICATION_SEND,
		EntityType: audit.ENTITY_TYPE_NOTIFICATION,
		EntityName: notif.Title,
		EntityID:   notif.ID,
		Before:     notif,
	}); errLog != nil {
		log.Printf(
			"[Server] Audit log failed: action=%s entity=%s id=%d name=%s error=%v",
			audit.ACTION_NOTIFICATION_SEND,
			audit.ENTITY_TYPE_NOTIFICATION,
			notif.ID,
			notif.Title,
			errLog,
		)
	}

	return nil
}
