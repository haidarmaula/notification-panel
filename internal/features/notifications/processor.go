package notifications

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/kafka"
)

// targetFetchLimit bounds the number of notification_target rows fetched
// for a single notification during target expansion.
//
// A notification with more target rows than this limit has the excess
// silently truncated by the underlying query. The expansion loop logs a
// warning when the limit is reached so that the truncation is observable
// in the worker logs rather than hidden — see finding H29 in
// code-review.md for the original defect. A future revision that needs
// unbounded target sets should replace the single-shot fetch with a
// paginated loop.
const targetFetchLimit = 10000

// audienceFetchLimit bounds the number of users or segment members fetched
// for a single BROADCAST or SEGMENT target.
//
// As with targetFetchLimit, the bound is defensive rather than operational:
// it exists to keep a runaway query from exhausting worker memory, not to
// cap the campaign size the system is designed to support. The expansion
// loop logs a warning when the limit is reached, closing the silent-
// truncation half of finding H29 in code-review.md.
//
// The value is deliberately large. At the current scale it will never be
// reached for a normal campaign; if it is reached, the campaign has
// outgrown the single-shot fan-out strategy and the worker should be
// reworked to stream audience members rather than materialise them.
const audienceFetchLimit = 1000000

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
// dispatching a single batched request to the configured provider, recording
// one delivery row per provider result, and publishing one DeliveryUpdated
// event for each delivery row successfully persisted.
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
// ProcessSendRequested is idempotent under Kafka's at-least-once delivery:
// the first thing it does after loading the notification is decide whether
// the notification has already been processed, and if so, return without
// dispatching again. The decision treats COMPLETED and FAILED as terminal
// outright, and treats PROCESSING as terminal only when at least one
// delivery row already exists for the notification — which indicates a
// predecessor reached the delivery-recording stage before crashing. A
// PROCESSING notification with no delivery rows is retried, on the
// assumption that the predecessor crashed before it dispatched anything.
//
// This closes finding H27 in code-review.md. A previous revision did not
// inspect PROCESSING at all, so a crashed predecessor's notification was
// re-dispatched unconditionally, producing duplicate pushes to every
// device. The current behaviour trades a small risk of lost delivery (a
// crash between the provider call and the first delivery insert) for the
// certainty of no duplicate delivery.
//
// The idempotency is still not transactionally airtight: the check and the
// dispatch are not atomic against a concurrent invocation for the same
// notification ID. See "Concurrency" below.
//
// # Delivery-row atomicity
//
// The delivery rows are inserted one at a time, each followed by a Kafka
// publish. Neither the insert loop nor the publish is wrapped in a
// transaction, and a mid-loop crash leaves a partial set of delivery rows
// in the database. On the next attempt the idempotency guard (above)
// detects the partial set and skips the notification, so the unrecorded
// deliveries are lost rather than duplicated. A future revision that needs
// at-most-once with no loss should record delivery intents before
// dispatching to the provider, then mark them confirmed afterwards.
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
// Every parameter must be non-nil; none is validated, so a nil dependency
// will panic on the first ProcessSendRequested call rather than at
// construction. This legacy processor is not wired by cmd/worker; the
// protected worker uses internal/dispatch.
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
// and updates the notification status. Idempotent under Kafka at-least-once
// semantics: skips COMPLETED and FAILED outright, and skips PROCESSING when
// delivery rows already exist. On unrecoverable failure the notification
// status is set to FAILED.
//
// # Flow
//
//  1. Load the notification row by event.NotificationID. A missing row
//     aborts with a wrapped error; the caller should log and drop the
//     event (the row will not reappear).
//  2. Decide whether the notification has already been processed. See
//     "Idempotency check" below for the exact rules.
//  3. Transition the notification to "PROCESSING" via
//     UpdateNotificationStatus. A failure here aborts.
//  4. Resolve the notification's targets to a deduplicated set of user IDs
//     via expandTargets. A failure here sets the status to "FAILED" and
//     aborts with a wrapped error.
//  5. If the resolved user set is empty, transition to "COMPLETED" and
//     return nil. Note that the notification is marked COMPLETED even
//     though no delivery occurred — for a broadcast against a system with
//     no active users, a segment with no members, or a notification whose
//     only target is of an unimplemented type, this is the path taken.
//  6. Fetch the device tokens for the resolved users via
//     DeviceTokenRepository.ListByUserIDs. A failure here sets status to
//     FAILED. If the token set is empty (no active devices), transition
//     to COMPLETED and return nil.
//  7. Build a SendRequest from the notification and the token set, and
//     invoke provider.Send. A failure here sets status to FAILED.
//  8. For each SendResult, insert a notification_deliveries row and, on
//     success, publish a DeliveryUpdated event. Insert and publish errors
//     are logged and skipped — they do not abort the loop.
//  9. Transition the notification to "COMPLETED" and return nil.
//
// # Idempotency check
//
// The decision at step 2 distinguishes three cases:
//
//   - Status COMPLETED or FAILED: the notification is terminal and is
//     skipped unconditionally. Returning nil (rather than an error) tells
//     the Kafka consumer to commit the offset for this event, so the
//     duplicate is not delivered again.
//
//   - Status PROCESSING with at least one delivery row present: a
//     predecessor reached the delivery-recording stage before crashing.
//     The notification is skipped, on the assumption that re-dispatching
//     would duplicate the pushes that were already sent. The unrecorded
//     remainder is lost rather than duplicated.
//
//   - Status PROCESSING with no delivery rows: a predecessor crashed
//     before any delivery was recorded, or the same event is being
//     processed for the first time. Either way, dispatching now is safe.
//
//   - Any other status (QUEUED, SCHEDULED, DRAFT, or an unrecognised
//     value): the notification is processed. QUEUED is the normal case —
//     the scheduler sets it before publishing the event — and the other
//     values are processed defensively so that a notification that
//     somehow entered the pipeline from an unexpected state is not
//     permanently stranded.
//
// This behaviour closes finding H27 in code-review.md. A previous revision
// recognised only COMPLETED and FAILED, so a notification left in
// PROCESSING by a crashed predecessor was re-dispatched in full, producing
// duplicate pushes to every device.
//
// # Provider contract dependence
//
// The processor depends on the provider returning one SendResult per input
// token, in the same order as the SendRequest's Tokens slice, and carrying
// each token's PushToken in the corresponding result. Both providers now
// honour this contract: FCMProvider has always done so, and
// OneSignalProvider was corrected in the H19–H21 pass. A provider that
// violates the contract will cause mis-attributed delivery rows; the
// attribution logic below emits a warning when it cannot find a match.
//
// # Delivery-update publication
//
// A DeliveryUpdated event is published after its corresponding delivery row
// has been persisted. The database row remains the source of truth. A publish
// failure is logged but does not fail the Kafka send-request message: returning
// an error after a provider call would cause the worker's at-least-once loop to
// retry the entire notification and potentially dispatch duplicate pushes.
// This best-effort boundary can lose analytics events; a transactional outbox
// is required for durable, exactly-once database-to-Kafka propagation.
//
// # Device token attribution
//
// Each delivery row is attributed to a specific device_tokens row by
// matching the SendResult's PushToken against the tokens slice that was
// dispatched. The push token is the identity that was actually sent to the
// provider, so the match is exact and every delivery row for a multi-device
// user is correctly attributed to the specific device the provider
// reported on.
//
// This closes finding H28 in code-review.md. A previous revision matched on
// UserID and broke at the first hit, so every delivery row for a multi-
// device user was attributed to the same device regardless of which device
// the provider had actually delivered to.
//
// When no match is found — which should not happen with a compliant
// provider but is possible with a misbehaving one — deviceTokenID remains
// 0, the row is inserted with a zero foreign key, and a warning is logged
// naming the user and push token so the mismatch is visible. The schema's
// foreign-key behaviour determines whether the insert is rejected or
// silently stores an invalid reference.
//
// # Failure semantics
//
// Three failure paths set the notification status to "FAILED":
// expandTargets failure, token-fetch failure, and provider-send failure.
// Two no-work paths set it to "COMPLETED": an empty resolved user set and
// an empty active-device-token set. A failure of one of those compensating
// status updates is logged rather than silently discarded, so a transient
// database error while marking a notification FAILED or COMPLETED remains
// visible in the worker logs while the original error is still returned to
// the caller.
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

	// Idempotency: decide whether this notification has already been
	// processed. See the "Idempotency check" section of the method
	// documentation for the full decision table.
	if skip, err := p.alreadyProcessed(ctx, notif); err != nil {
		return err
	} else if skip {
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
		if statusErr := p.notifRepo.UpdateStatus(ctx, sqlc.UpdateNotificationStatusParams{
			ID:     notif.ID,
			Status: "FAILED",
		}); statusErr != nil {
			log.Printf("[Worker] Failed to mark notification %d FAILED after target expansion failure: %v", notif.ID, statusErr)
		}
		return fmt.Errorf("expand targets: %w", err)
	}

	// No users targeted → nothing to send.
	if len(userIDs) == 0 {
		log.Printf("[Worker] No users targeted, marking as COMPLETED")
		if statusErr := p.notifRepo.UpdateStatus(ctx, sqlc.UpdateNotificationStatusParams{
			ID:     notif.ID,
			Status: "COMPLETED",
		}); statusErr != nil {
			log.Printf("[Worker] Failed to mark notification %d COMPLETED after empty user set: %v", notif.ID, statusErr)
		}
		return nil
	}

	tokens, err := p.deviceRepo.ListByUserIDs(ctx, userIDs)
	if err != nil {
		if statusErr := p.notifRepo.UpdateStatus(ctx, sqlc.UpdateNotificationStatusParams{
			ID:     notif.ID,
			Status: "FAILED",
		}); statusErr != nil {
			log.Printf("[Worker] Failed to mark notification %d FAILED after device-token fetch failure: %v", notif.ID, statusErr)
		}
		return fmt.Errorf("fetch device tokens: %w", err)
	}

	if len(tokens) == 0 {
		log.Printf("[Worker] No active device tokens, marking as COMPLETED")
		if statusErr := p.notifRepo.UpdateStatus(ctx, sqlc.UpdateNotificationStatusParams{
			ID:     notif.ID,
			Status: "COMPLETED",
		}); statusErr != nil {
			log.Printf("[Worker] Failed to mark notification %d COMPLETED after empty token set: %v", notif.ID, statusErr)
		}
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
		if statusErr := p.notifRepo.UpdateStatus(ctx, sqlc.UpdateNotificationStatusParams{
			ID:     notif.ID,
			Status: "FAILED",
		}); statusErr != nil {
			log.Printf("[Worker] Failed to mark notification %d FAILED after provider send failure: %v", notif.ID, statusErr)
		}
		return fmt.Errorf("provider send: %w", err)
	}

	// Record each delivery result. The provider is expected to return one
	// result per input token, each carrying the token's PushToken; the
	// token is the identity we use to attribute the delivery to a specific
	// device_tokens row.
	for _, r := range results {
		var deviceTokenID int64
		for _, t := range tokens {
			if t.PushToken == r.PushToken {
				deviceTokenID = t.ID
				break
			}
		}
		if deviceTokenID == 0 {
			log.Printf(
				"[Worker] Could not attribute delivery to a device token for user %d (push token %q); recording with device_token_id=0",
				r.UserID,
				r.PushToken,
			)
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
			continue
		}

		update := deliveryUpdatedEvent(notif.ID, "ONESIGNAL", r, time.Now())
		if err := p.producer.PublishDeliveryUpdated(ctx, update); err != nil {
			log.Printf("[Worker] Failed to publish delivery update for notification %d, user %d: %v", notif.ID, r.UserID, err)
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

// alreadyProcessed reports whether the supplied notification has already
// been processed and should be skipped by ProcessSendRequested.
//
// The function is the idempotency guard for Kafka's at-least-once delivery
// contract. It returns (true, nil) when the notification must not be
// re-dispatched, (false, nil) when processing may proceed, and a non-nil
// error only when the decision requires a database read that failed.
//
// # Parameter type
//
// notif is the row type returned by NotificationRepository.FindByID
// (sqlc.GetNotificationByIDRow), not the base sqlc.Notification model.
// The two diverge because the underlying query joins in additional context
// beyond the notification table's own columns; the fields this function
// reads — ID and Status — are present on both. A future revision that
// changes the repository's return type must update this signature in
// lockstep.
//
// # Decision table
//
//	COMPLETED, FAILED                     → skip (terminal)
//	PROCESSING, delivery rows > 0         → skip (predecessor reached recording)
//	PROCESSING, delivery rows == 0        → proceed (predecessor crashed early)
//	any other status                      → proceed
//
// The PROCESSING case queries the delivery repository to distinguish a
// crash-early predecessor (safe to retry) from a crash-late predecessor
// (skip to avoid duplicate pushes). The query is a COUNT against an
// indexed column and is cheap relative to the provider call that would
// otherwise follow.
//
// # Errors
//
// The only error source is the delivery count query for a PROCESSING
// notification. A failure there is returned wrapped with the prefix
// "count existing deliveries: " so the caller (ProcessSendRequested) can
// propagate it without further decoration; the notification is left in its
// current state and the Kafka consumer will retry the event.
//
// # Concurrency
//
// Safe for concurrent use. The function holds no state between calls and
// performs only read-only repository queries.
func (p *Processor) alreadyProcessed(ctx context.Context, notif sqlc.GetNotificationByIDRow) (bool, error) {
	switch notif.Status {
	case "COMPLETED", "FAILED":
		log.Printf("[Worker] Notification %d already processed (status=%s), skipping", notif.ID, notif.Status)
		return true, nil
	case "PROCESSING":
		existing, err := p.deliveryRepo.CountByNotification(ctx, notif.ID)
		if err != nil {
			return false, fmt.Errorf("count existing deliveries: %w", err)
		}
		if existing > 0 {
			log.Printf(
				"[Worker] Notification %d is PROCESSING with %d delivery rows from a previous attempt, skipping to avoid duplicate delivery",
				notif.ID,
				existing,
			)
			return true, nil
		}
		log.Printf("[Worker] Notification %d is PROCESSING with no delivery rows; treating as a crash-early retry", notif.ID)
		return false, nil
	default:
		return false, nil
	}
}

// deliveryUpdatedEvent converts a provider result into the analytics event
// emitted after its delivery row is stored.
//
// provider is the normalized provider label persisted with the delivery. A
// provider error always yields FAILED and includes its human-readable reason;
// otherwise the provider's result status is preserved (typically SENT). The
// helper is deterministic for a supplied occurredAt and safe for concurrent
// use because it has no mutable state.
func deliveryUpdatedEvent(notificationID int64, provider string, result SendResult, occurredAt time.Time) kafka.DeliveryUpdated {
	status := result.Status
	failedReason := ""
	if result.Error != nil {
		status = "FAILED"
		failedReason = result.Error.Error()
	}

	return kafka.DeliveryUpdated{
		NotificationID:    notificationID,
		UserID:            result.UserID,
		Provider:          provider,
		ProviderMessageID: result.ProviderMessageID,
		Status:            status,
		OccurredAt:        occurredAt,
		FailedReason:      failedReason,
	}
}

// expandTargets resolves all user IDs targeted by the notification.
// Supported target types: BROADCAST, SEGMENT, INDIVIDUAL. Deduplicates results.
//
// # Flow
//
// The method reads the notification's target rows — paginated at offset 0
// with a limit of targetFetchLimit — and iterates them, accumulating user
// IDs according to each row's target type:
//
//   - BROADCAST: every user with status "ACTIVE", fetched via
//     ListByStatus with a limit of audienceFetchLimit.
//   - SEGMENT: every member of the segment identified by the row's
//     segment_id, fetched via ListBySegment with a limit of
//     audienceFetchLimit.
//   - INDIVIDUAL: the single user_id on the row, if it is non-NULL.
//   - UPLOAD: not implemented. The case exists in the switch and
//     performs a `continue` with a TODO comment; the row contributes no
//     users.
//
// After accumulation, the method deduplicates the ID list so that a user
// targeted by more than one strategy appears once. The dedup uses a boolean
// set and preserves input order.
//
// # Limit handling
//
// Both the target fetch and the audience fetches are bounded by named
// constants (targetFetchLimit and audienceFetchLimit). When a fetch returns
// exactly as many rows as its limit, the method logs a warning naming the
// notification and the affected fetch, on the reasoning that a full page
// almost always means the underlying set was larger and the excess was
// silently dropped by the query's LIMIT clause.
//
// This closes finding H29 in code-review.md. A previous revision used
// unnamed literals for the same bounds and logged nothing when they were
// reached, so a truncated audience was indistinguishable from a complete
// one in the worker logs.
//
// The warning does not abort the expansion: the users that were returned
// are still targeted, and the notification proceeds with a smaller
// audience than the operator may have intended. A future revision that
// needs to fail loudly on truncation should replace the single-shot fetch
// with a paginated loop and remove the limits.
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
	targets, err := p.targetRepo.ListByNotification(ctx, notificationID, 0, targetFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("list targets: %w", err)
	}
	if int32(len(targets)) == targetFetchLimit {
		log.Printf(
			"[Worker] Notification %d: target fetch returned the maximum of %d rows; any additional targets were truncated",
			notificationID,
			targetFetchLimit,
		)
	}

	var userIDs []int64
	for _, t := range targets {
		switch t.TargetType {
		case "BROADCAST":
			users, err := p.userRepo.ListByStatus(ctx, "ACTIVE", 0, audienceFetchLimit)
			if err != nil {
				return nil, fmt.Errorf("list active users: %w", err)
			}
			if int32(len(users)) == audienceFetchLimit {
				log.Printf(
					"[Worker] Notification %d: BROADCAST audience fetch returned the maximum of %d users; any additional users were truncated",
					notificationID,
					audienceFetchLimit,
				)
			}
			for _, u := range users {
				userIDs = append(userIDs, u.ID)
			}
		case "SEGMENT":
			if !t.SegmentID.Valid {
				continue
			}
			members, err := p.memberRepo.ListBySegment(ctx, t.SegmentID.Int64, 0, audienceFetchLimit)
			if err != nil {
				return nil, fmt.Errorf("list segment members: %w", err)
			}
			if int32(len(members)) == audienceFetchLimit {
				log.Printf(
					"[Worker] Notification %d: SEGMENT %d member fetch returned the maximum of %d rows; any additional members were truncated",
					notificationID,
					t.SegmentID.Int64,
					audienceFetchLimit,
				)
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
