package notifications

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
)

type Processor struct {
	notifRepo    *repository.NotificationRepository
	targetRepo   *repository.NotificationTargetRepository
	deliveryRepo *repository.NotificationDeliveryRepository
	deviceRepo   *repository.DeviceTokenRepository
	userRepo     *repository.UserRepository
	segmentRepo  *repository.SegmentRepository
	memberRepo   *repository.SegmentMemberRepository
	provider     NotificationProvider
	producer     *kafka.Producer
}

func NewProcessor(
	notifRepo *repository.NotificationRepository,
	targetRepo *repository.NotificationTargetRepository,
	deliveryRepo *repository.NotificationDeliveryRepository,
	deviceRepo *repository.DeviceTokenRepository,
	userRepo *repository.UserRepository,
	segmentRepo *repository.SegmentRepository,
	memberRepo *repository.SegmentMemberRepository,
	provider NotificationProvider,
	producer *kafka.Producer,
) *Processor {
	return &Processor{
		notifRepo:    notifRepo,
		targetRepo:   targetRepo,
		deliveryRepo: deliveryRepo,
		deviceRepo:   deviceRepo,
		userRepo:     userRepo,
		segmentRepo:  segmentRepo,
		memberRepo:   memberRepo,
		provider:     provider,
		producer:     producer,
	}
}

func (p *Processor) ProcessSendRequested(ctx context.Context, event kafka.NotificationSendRequested) error {
	log.Printf("[Worker] Processing notification %d", event.NotificationID)

	notif, err := p.notifRepo.FindByID(ctx, event.NotificationID)
	if err != nil {
		return fmt.Errorf("fetch notification: %w", err)
	}

	if notif.Status == "COMPLETED" || notif.Status == "FAILED" {
		log.Printf("[Worker] Notification %d already processed, skipping", event.NotificationID)
		return nil
	}

	if err := p.notifRepo.UpdateStatus(ctx, sqlc.UpdateNotificationStatusParams{
		ID:     notif.ID,
		Status: "PROCESSING",
	}); err != nil {
		return fmt.Errorf("update status to PROCESSING: %w", err)
	}

	userIDs, err := p.expandTargets(ctx, event.NotificationID)
	if err != nil {
		_ = p.notifRepo.UpdateStatus(ctx, sqlc.UpdateNotificationStatusParams{
			ID:     notif.ID,
			Status: "FAILED",
		})
		return fmt.Errorf("expand targets: %w", err)
	}

	if len(userIDs) == 0 {
		log.Printf("[Worker] No users targeted, marking as COMPLETED")
		_ = p.notifRepo.UpdateStatus(ctx, sqlc.UpdateNotificationStatusParams{
			ID:     notif.ID,
			Status: "COMPLETED",
		})
		return nil
	}

	tokens, err := p.deviceRepo.ListByUserIDs(ctx, userIDs)
	if err != nil {
		_ = p.notifRepo.UpdateStatus(ctx, sqlc.UpdateNotificationStatusParams{
			ID:     notif.ID,
			Status: "FAILED",
		})
		return fmt.Errorf("fetch device tokens: %w", err)
	}

	if len(tokens) == 0 {
		log.Printf("[Worker] No active device tokens, marking as COMPLETED")
		_ = p.notifRepo.UpdateStatus(ctx, sqlc.UpdateNotificationStatusParams{
			ID:     notif.ID,
			Status: "COMPLETED",
		})
		return nil
	}

	req := SendRequest{
		NotificationID: notif.ID,
		Title:          notif.Title,
		Body:           notif.Body,
		Tokens:         make([]DeviceTokenInfo, len(tokens)),
	}
	for i, t := range tokens {
		req.Tokens[i] = DeviceTokenInfo{
			UserID:    t.UserID,
			PushToken: t.PushToken,
			Platform:  t.Platform,
		}
	}

	results, err := p.provider.Send(ctx, req)
	if err != nil {
		_ = p.notifRepo.UpdateStatus(ctx, sqlc.UpdateNotificationStatusParams{
			ID:     notif.ID,
			Status: "FAILED",
		})
		return fmt.Errorf("provider send: %w", err)
	}

	// Build and insert deliveries
	for _, r := range results {
		var deviceTokenID int64
		for _, t := range tokens {
			if t.UserID == r.UserID {
				deviceTokenID = t.ID
				break
			}
		}
		status := r.Status
		if r.Error != nil {
			status = "FAILED"
		}
		_, err := p.deliveryRepo.Create(ctx, sqlc.CreateNotificationDeliveryParams{
			NotificationID:    notif.ID,
			UserID:            r.UserID,
			DeviceTokenID:     deviceTokenID,
			Provider:          "FCM",
			ProviderMessageID: pgtype.Text{String: r.ProviderMessageID, Valid: r.ProviderMessageID != ""},
			Status:            status,
		})
		if err != nil {
			log.Printf("[Worker] Failed to insert delivery for user %d: %v", r.UserID, err)
		}
	}

	if err := p.notifRepo.UpdateStatus(ctx, sqlc.UpdateNotificationStatusParams{
		ID:     notif.ID,
		Status: "COMPLETED",
	}); err != nil {
		return fmt.Errorf("update status to COMPLETED: %w", err)
	}

	log.Printf("[Worker] Notification %d processed successfully", event.NotificationID)
	return nil
}

func (p *Processor) expandTargets(ctx context.Context, notificationID int64) ([]int64, error) {
	targets, err := p.targetRepo.ListByNotification(ctx, notificationID, 0, 10000)
	if err != nil {
		return nil, fmt.Errorf("list targets: %w", err)
	}

	var userIDs []int64
	for _, t := range targets {
		switch t.TargetType {
		case "GLOBAL":
			users, err := p.userRepo.ListByStatus(ctx, "ACTIVE", 0, 1000000)
			if err != nil {
				return nil, fmt.Errorf("list active users: %w", err)
			}
			for _, u := range users {
				userIDs = append(userIDs, u.ID)
			}
		case "SEGMENT":
			if !t.SegmentID.Valid {
				continue
			}
			members, err := p.memberRepo.ListBySegment(ctx, t.SegmentID.Int64, 0, 1000000)
			if err != nil {
				return nil, fmt.Errorf("list segment members: %w", err)
			}
			for _, m := range members {
				userIDs = append(userIDs, m.UserID)
			}
		case "USER":
			if t.UserID.Valid {
				userIDs = append(userIDs, t.UserID.Int64)
			}
		case "UPLOAD":
			// TODO: implement later
			continue
		default:
			continue
		}
	}

	seen := make(map[int64]bool)
	unique := make([]int64, 0, len(userIDs))
	for _, id := range userIDs {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	return unique, nil
}
