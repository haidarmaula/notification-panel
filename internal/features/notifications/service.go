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
var (
	ErrStaffNotFoundOrInactive = errors.New("staff account not found or inactive")
	ErrNotificationNotFound    = errors.New("notification not found")
	ErrNotificationNotDraft    = errors.New("notification must be in DRAFT status")
	ErrInvalidTargetType       = errors.New("invalid target type")
	ErrTemplateNotFound        = errors.New("template not found")
	ErrSegmentNotFound         = errors.New("segment not found")
	ErrSegmentIDRequired       = errors.New("segment_id required for SEGMENT type")
	ErrInvalidScheduledTime    = errors.New("scheduled time must be in the future")
	ErrCannotDeleteSent        = errors.New("cannot delete sent notification")
	ErrTargetsRequired         = errors.New("user_ids required for INDIVIDUAL type")
)

// NotificationService provides business logic for notifications.
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
func (s *NotificationService) List(ctx context.Context, page, limit int32, status, targetType, keyword string) ([]NotificationListItem, int64, error) {
	offset := (page - 1) * limit

	if status != "" || targetType != "" || keyword != "" {
		rows, err := s.notifRepo.ListWithFilters(ctx, status, targetType, keyword, offset, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("list notifications with filters: %w", err)
		}
		items := make([]NotificationListItem, len(rows))
		for i, row := range rows {
			items[i] = NotificationListItem{
				ID:          row.ID,
				Title:       row.Title,
				Type:        row.TargetType, // sekarang string
				Status:      row.Status,
				CreatedBy:   row.CreatedByName,
				ScheduledAt: &row.ScheduledAt.Time,
				SentAt:      nil,
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
		items[i] = NotificationListItem{
			ID:          row.ID,
			Title:       row.Title,
			Type:        "BROADCAST",
			Status:      row.Status,
			CreatedBy:   row.CreatedByName,
			ScheduledAt: &row.ScheduledAt.Time,
			SentAt:      nil,
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
type CreateParams struct {
	Title       string
	Body        string
	TemplateID  *int64
	TargetType  string
	SegmentID   *int64
	UserIDs     []int64
	ScheduledAt *time.Time
	CreatedBy   int64
}

// Create creates a new notification draft.
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
type UpdateParams struct {
	Title       *string
	Body        *string
	TemplateID  *int64
	ScheduledAt *time.Time
}

// Update updates a draft notification.
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

	// Publish event to Kafka
	event := kafka.NotificationSendRequested{
		NotificationID: id,
		RequestedAt:    time.Now(),
	}
	if err := s.producer.PublishSendRequested(ctx, event); err != nil {
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
