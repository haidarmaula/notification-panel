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

// Processor orchestrates the end-to-end delivery of a notification.
//
// It is the worker-side counterpart of NotificationService: where the service
// handles the API's create/update/delete/send requests, the processor
// consumes the NotificationSendRequested events those requests produce and
// performs the actual delivery fan-out. It is instantiated once in
// cmd/worker and invoked from the Kafka consume loop for every event on the
// send topic.
//
// # Delivery model
//
// For each event the processor transitions the notification through the
// states PROCESSING → COMPLETED (or FAILED), resolving the notification's
// targets into a set of user IDs, expanding those users into device tokens,
// dispatching a single batched request to the configured provider, and
// recording one delivery row per provider result. It does not emit a
// NotificationDeliveryUpdated event to the update topic — see the
// "Missing delivery-update publication" section on ProcessSendRequested.
//
// # Status vocabulary
//
// The processor reads and writes the raw strings "PROCESSING", "COMPLETED",
// "FAILED", and "QUEUED" directly, without referring to the
// NotificationStatus constants declared in model.go. The two vocabularies
// have drifted: model.go declares DRAFT, SCHEDULED, SENDING, SENT, and
// FAILED, of which only FAILED matches what the processor writes. Callers
// must not assume the constants are authoritative for status values the
// processor writes.
//
// # Idempotency and at-least-once semantics
//
// ProcessSendRequested is intended to be idempotent under Kafka's
// at-least-once delivery: the first thing it does after loading the
// notification is check whether its status is terminal, and if so, return
// immediately. However, the idempotency check is partial — see
// ProcessSendRequested for the specific states it covers and the states it
// does not — and it does not protect against duplicate delivery rows for a
// notification that crashed mid-processing.
//
// # Missing transactional isolation
//
// The processor performs its state transitions and its delivery inserts as
// separate repository calls, with no enclosing database transaction.
// A failure between the provider dispatch and the delivery-recording loop
// leaves the notification in PROCESSING with no delivery rows; a retry
// would then re-dispatch to the provider, potentially double-delivering.
//
// # Concurrency
//
// The processor is designed to be invoked from a single consume goroutine
// per worker instance. Two concurrent invocations for the same
// notification ID will both pass the status check and both attempt
// fan-out; the resulting deliveries may be duplicated. Multi-replica
// deployments of the worker rely on the shared Kafka consumer group to
// ensure each event is processed by exactly one replica at a time, but
// they do not protect against the reprocessing that Kafka's at-least-once
// contract permits after a crash.
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

// NewProcessor creates a new Processor with all required dependencies.
//
// # Parameters
//
// Every parameter must be non-nil; none is validated, so a nil repository
// or provider will panic on the first ProcessSendRequested call rather than
// at construction. In practice the dependencies are wired by cmd/worker,
// which builds the repositories from the shared sqlc.Queries, constructs
// the provider (currently always OneSignalProvider), and creates the Kafka
// producer targeting the update topic.
//
// # Unused producer field
//
// The producer passed to this constructor is stored on the struct but is
// never referenced by ProcessSendRequested. The processor therefore does
// not publish delivery-update events despite the field's presence and
// despite the README's description of the worker as emitting such events.
// See ProcessSendRequested for the full implications.
//
// # Concurrency
//
// The constructor is cheap and stateless. The returned processor holds only
// references and is safe to share, provided callers respect the
// single-goroutine-per-notification constraint documented on the type.
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

// ProcessSendRequested handles a Kafka event to deliver a notification.
// It expands targets, fetches tokens, calls the provider, records deliveries,
// and updates the notification status. Idempotent: skips if already terminal.
// On failure, the notification status is set to FAILED.
//
// # Flow
//
//  1. Load the notification row by event.NotificationID. A missing row
//     aborts with a wrapped error; the caller should log and drop the
//     event (the row will not reappear).
//  2. Check the row's status for terminality. If the status is "COMPLETED"
//     or "FAILED", the method returns nil without further work — this is
//     the idempotency guard. See "Idempotency check" below for the states
//     it does and does not cover.
//  3. Transition the notification to "PROCESSING" via
//     UpdateNotificationStatus. A failure here aborts.
//  4. Resolve the notification's targets to a deduplicated set of user IDs
//     via expandTargets. A failure here sets the status to "FAILED" and
//     aborts with a wrapped error.
//  5. If the resolved user set is empty, transition to "COMPLETED" and
//     return nil. Note that the notification is marked COMPLETED even
//     though no delivery occurred — for a broadcast against a system with
//     no active users, or a segment with no members, or a notification
//     whose only target is of an unimplemented type, this is the path
//     taken.
//  6. Fetch the device tokens for the resolved users via
//     DeviceTokenRepository.ListByUserIDs. A failure here sets status to
//     FAILED. If the token set is empty (no active devices), transition
//     to COMPLETED and return nil.
//  7. Build a SendRequest from the notification and the token set, and
//     invoke provider.Send. A failure here sets status to FAILED.
//  8. For each SendResult, insert a notification_deliveries row. Insert
//     errors are logged and skipped — they do not abort the loop.
//  9. Transition the notification to "COMPLETED" and return nil.
//
// # Idempotency check
//
// The check at step 2 recognises only "COMPLETED" and "FAILED" as terminal.
// Several states that are also terminal in practice are not covered:
//
//   - A notification left in "PROCESSING" by a crashed predecessor is
//     re-processed, which re-dispatches the provider call and may create
//     duplicate delivery rows.
//   - A notification in "QUEUED" (set by the scheduler before publishing
//     the Kafka event) is processed, which is correct.
//   - A notification in "SENT" — which model.go declares as a terminal
//     status but which this processor never writes — would be re-processed.
//
// The processor is therefore safe against re-delivery of the same Kafka
// event only when the previous attempt completed successfully or failed
// with the FAILED transition applied. A crash between provider dispatch
// and the terminal status update results in a duplicate dispatch on retry.
//
// # Provider contract dependence
//
// The processor depends on the provider returning one SendResult per input
// token, in the same order as the SendRequest's Tokens slice. FCMProvider
// honours this contract; OneSignalProvider does not — it filters short
// tokens, so its result slice may be shorter than the input. When running
// against OneSignalProvider, tokens dropped by the provider's filter are
// silently lost: no delivery row is inserted for them, and they are not
// counted as failures.
//
// # Missing delivery-update publication
//
// Despite the producer field being wired in and despite the README
// describing the worker as emitting NotificationDeliveryUpdated events on
// the update topic for downstream analytics, this method never calls the
// producer. No delivery-update event is published from any code path in
// this package. Downstream consumers that expect those events will receive
// nothing; the only record of delivery outcomes is the
// notification_deliveries table.
//
// Adding the publish is straightforward — the field and the producer's
// PublishDeliveryUpdated method both exist — but the sequence around it
// (per-result publication, error handling, and the interaction with the
// COMPLETED status update) requires design work that has not been done.
//
// # Device token attribution
//
// When recording delivery rows, the method attempts to resolve each
// SendResult's user to a specific device_tokens row by iterating the
// tokens slice and matching on UserID. Because the tokens slice contains
// one entry per (user, device) pair, a user with multiple active devices
// will match only the first one — the iteration `break`s as soon as any
// token for that user is found.
//
// The practical consequence is that every delivery row for a multi-device
// user is recorded against the same device_tokens row (the first one
// encountered in the slice), regardless of which device the provider
// actually delivered to. Delivery attribution is therefore incorrect for
// any user with more than one device. Matching on PushToken instead of
// UserID would resolve this for providers that return per-device results,
// but the current OneSignalProvider returns the same result for every
// device in a batch, so even a correct match would not distinguish them.
//
// When no match is found — which cannot happen with a well-behaved
// provider but is possible with a misbehaving one — deviceTokenID remains
// 0 and the delivery row is inserted with a zero foreign key, which will
// either be rejected by a foreign-key constraint or silently insert an
// invalid reference.
//
// # Failure semantics
//
// Five paths set the notification status to "FAILED":
// expandTargets failure, token-fetch failure, provider-send failure, and
// the two "no work to do" branches do NOT set FAILED (they set COMPLETED).
// A failure of the status update itself — for example, a database outage
// that prevents writing FAILED — is silently ignored: the method updates
// status without checking the returned error in most branches, so a
// subsequent retry will see whatever status was previously written.
//
// # Concurrency
//
// Not safe to invoke concurrently for the same event.NotificationID. The
// Kafka consume loop in cmd/worker invokes this method serially from a
// single goroutine, so the constraint is honoured in practice; a future
// revision that processes messages in parallel must add a per-notification
// lock or a database-level claim.
func (p *Processor) ProcessSendRequested(ctx context.Context, event kafka.NotificationSendRequested) error {
	log.Printf("[Worker] Processing notification %d", event.NotificationID)

	notif, err := p.notifRepo.FindByID(ctx, event.NotificationID)
	if err != nil {
		return fmt.Errorf("fetch notification: %w", err)
	}

	// Idempotency: skip if already processed.
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

	// No users targeted → nothing to send.
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
		ClickAction:    "NOTIFICATION_CLICK",
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

	// Record each delivery result.
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
			Provider:          "ONESIGNAL",
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

// expandTargets resolves all user IDs targeted by the notification.
// Supported target types: BROADCAST, SEGMENT, INDIVIDUAL. Deduplicates results.
//
// # Flow
//
// The method reads the notification's target rows — paginated at offset 0
// with a limit of 10,000 — and iterates them, accumulating user IDs
// according to each row's target type:
//
//   - BROADCAST: every user with status "ACTIVE", fetched via
//     ListByStatus with a limit of 1,000,000.
//   - SEGMENT: every member of the segment identified by the row's
//     segment_id, fetched via ListBySegment with a limit of 1,000,000.
//   - INDIVIDUAL: the single user_id on the row, if it is non-NULL.
//   - UPLOAD: not implemented. The case exists in the switch and
//     performs a `continue` with a TODO comment; the row contributes no
//     users.
//
// After accumulation, the method deduplicates the ID list so that a user
// targeted by more than one strategy appears once. The dedup uses a boolean
// set and preserves input order.
//
// # Effective limits
//
// The pagination limits used here are effectively unbounded. A BROADCAST
// against a system with more than one million active users will silently
// truncate to the first million; a segment of similar size will do the
// same. Neither limit is derived from configuration, and neither is
// logged when it truncates. For the current scale this is a latent issue
// rather than an active one, but it will become active without warning.
//
// The 10,000-target limit on the initial fetch is more likely to be
// reached in practice than either of the million-row limits: a
// notification with more than 10,000 target rows — a plausible count for
// an INDIVIDUAL notification addressed to a large audience — will have
// the excess targets silently dropped.
//
// # UPLOAD targets are silently ignored
//
// A target row of type "UPLOAD" contributes no users and produces no
// warning. A notification whose only target is UPLOAD is therefore marked
// COMPLETED by the caller with zero deliveries — the caller's "no users
// targeted" branch takes it. From the operator's point of view the
// notification succeeded; in fact nothing was sent.
//
// # Nil segment_id and user_id
//
// A SEGMENT target whose segment_id is NULL, or an INDIVIDUAL target
// whose user_id is NULL, is skipped by an `if !t.X.Valid { continue }`
// guard. The row is not an error; it simply contributes nothing. This
// matches the repository's nullable-column model but means a malformed
// target row is silently absorbed rather than surfaced.
//
// # Errors
//
// Any repository error encountered while expanding a target is returned
// wrapped. The caller — ProcessSendRequested — treats a non-nil error as
// a fatal failure of the notification and transitions it to FAILED.
//
// # Concurrency
//
// Safe for concurrent invocation on distinct notification IDs. No shared
// state is mutated; the only shared fields on the receiver are the
// repository pointers, which are concurrency-safe.
func (p *Processor) expandTargets(ctx context.Context, notificationID int64) ([]int64, error) {
	targets, err := p.targetRepo.ListByNotification(ctx, notificationID, 0, 10000)
	if err != nil {
		return nil, fmt.Errorf("list targets: %w", err)
	}

	var userIDs []int64
	for _, t := range targets {
		switch t.TargetType {
		case "BROADCAST":
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
		case "INDIVIDUAL":
			if t.UserID.Valid {
				userIDs = append(userIDs, t.UserID.Int64)
			}
		case "UPLOAD":
			// TODO: implement later
			continue
		}
	}

	// Deduplicate to avoid multiple deliveries to same user.
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
