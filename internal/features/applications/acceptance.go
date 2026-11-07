package applications

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/audit"
	"hello/internal/authz"
	"hello/internal/database/repository"
	"hello/internal/database/sqlc"
	"hello/internal/httprequest"
	"hello/pkg/response"
)

var (
	errSendHeld         = errors.New("campaign acceptance is held")
	errAudienceNotReady = errors.New("campaign audience is not ready")
	errKeyConflict      = errors.New("idempotency key is already in use")
	errInvalidSendKey   = errors.New("invalid idempotency key")
	errInvalidSchedule  = errors.New("invalid campaign schedule")
)

type Acceptance struct {
	ID         string             `json:"id"`
	Status     string             `json:"status"`
	QueuedAt   pgtype.Timestamptz `json:"queued_at"`
	DeadlineAt pgtype.Timestamptz `json:"deadline_at"`
}

type ScheduledCampaign struct {
	ID          string             `json:"id"`
	Status      string             `json:"status"`
	ScheduledAt pgtype.Timestamptz `json:"scheduled_at"`
	DeadlineAt  pgtype.Timestamptz `json:"deadline_at"`
}

func sendKey(campaignID int64, value string) (string, string, error) {
	if value == "" {
		return "derived", strconv.FormatInt(campaignID, 10), nil
	}
	if !utf8.ValidString(value) || len([]rune(value)) > 255 {
		return "", "", errInvalidSendKey
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", "", errInvalidSendKey
		}
	}
	return "client", value, nil
}

func acceptanceSnapshot(id int64, queued, deadline pgtype.Timestamptz) Acceptance {
	return Acceptance{strconv.FormatInt(id, 10), "QUEUED", queued, deadline}
}

func lockAcceptanceScope(ctx context.Context, q *sqlc.Queries, id pgtype.UUID, permission authz.Permission) (sqlc.LockNotificationRuntimeRow, sqlc.Application, authz.ApplicationPrincipal, error) {
	runtime, err := q.LockNotificationRuntime(ctx)
	if err != nil {
		return runtime, sqlc.Application{}, authz.ApplicationPrincipal{}, err
	}
	app, err := repository.NewApplicationRepository(q).LockByPublicID(ctx, id)
	if err != nil {
		return runtime, app, authz.ApplicationPrincipal{}, appError(err)
	}
	actorID, ok := audit.GetStaffID(ctx)
	if !ok {
		return runtime, app, authz.ApplicationPrincipal{}, authz.ErrUnauthenticated
	}
	if _, err := q.LockAcceptanceStaff(ctx, actorID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = authz.ErrUnauthenticated
		}
		return runtime, app, authz.ApplicationPrincipal{}, err
	}
	scope, err := authz.NewApplicationAuthorizer(q).RequireApplication(ctx, id, permission)
	return runtime, app, scope, err
}

func acceptancePolicy(ctx context.Context, q *sqlc.Queries, runtime sqlc.LockNotificationRuntimeRow, applicationID int64) (sqlc.GetApplicationDispatchPolicyRow, error) {
	if runtime.Sealed || runtime.MigrationHold {
		return sqlc.GetApplicationDispatchPolicyRow{}, errSendHeld
	}
	policy, err := q.GetApplicationDispatchPolicy(ctx, applicationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return policy, errSendHeld
	}
	if err != nil {
		return policy, err
	}
	if policy.HoldNew {
		return policy, errSendHeld
	}
	if !policy.CoverageApproved {
		return policy, errAudienceNotReady
	}
	return policy, nil
}

// AcceptCampaign persists the key, state, deadline, audit and send intent before
// returning success. Replay is an authority-checked immutable response snapshot.
func (s *Service) AcceptCampaign(ctx context.Context, id pgtype.UUID, campaignID int64, key string) (Acceptance, bool, error) {
	if _, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.NotificationsSend); err != nil {
		return Acceptance{}, false, err
	}
	if campaignID <= 0 {
		return Acceptance{}, false, errInvalidCampaignID
	}
	source, key, err := sendKey(campaignID, key)
	if err != nil {
		return Acceptance{}, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var result Acceptance
	replayed := false
	err = s.write(ctx, func(q *sqlc.Queries) error {
		replayed = false
		runtime, app, scope, err := lockAcceptanceScope(ctx, q, id, authz.NotificationsSend)
		if err != nil {
			return err
		}
		stored, err := q.GetSendKey(ctx, sqlc.GetSendKeyParams{ApplicationID: app.ID, StaffUserID: scope.Staff.ID, KeySource: source, Key: key})
		if err == nil && stored.Unexpired {
			if stored.NotificationID != campaignID {
				return errKeyConflict
			}
			result = acceptanceSnapshot(campaignID, stored.QueuedAt, stored.DeadlineAt)
			replayed = true
			return nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			removed, err := q.DeleteSendKey(ctx, sqlc.DeleteSendKeyParams{ApplicationID: app.ID, StaffUserID: scope.Staff.ID, KeySource: source, Key: key})
			if err != nil {
				return err
			}
			if removed != 1 {
				return errKeyConflict
			}
		}
		policy, err := acceptancePolicy(ctx, q, runtime, app.ID)
		if err != nil {
			return err
		}
		campaign, err := q.LockCampaignAcceptance(ctx, sqlc.LockCampaignAcceptanceParams{ApplicationID: app.ID, CampaignID: campaignID})
		if err != nil {
			return campaignLookupError(err)
		}
		if campaign.Status != "DRAFT" {
			return errCampaignNotDraft
		}
		ready, err := q.CampaignAcceptanceReady(ctx, sqlc.CampaignAcceptanceReadyParams{ApplicationID: app.ID, CampaignID: campaignID, PilotCohort: policy.PilotCohort})
		if err != nil {
			return err
		}
		if !ready {
			return errAudienceNotReady
		}
		queued, err := q.QueueScopedCampaign(ctx, sqlc.QueueScopedCampaignParams{ApplicationID: app.ID, CampaignID: campaignID, PushTtlSeconds: policy.PushTtlSeconds, OriginEpoch: runtime.OriginEpoch, SourceEpoch: runtime.SourceEpoch})
		if err != nil {
			return campaignLookupError(err)
		}
		if err := q.InsertSendOutbox(ctx, sqlc.InsertSendOutboxParams{ApplicationID: app.ID, NotificationID: campaignID, RequestedAt: queued.QueuedAt, DeadlineAt: queued.DeadlineAt, OriginEpoch: runtime.OriginEpoch, SourceEpoch: runtime.SourceEpoch}); err != nil {
			return err
		}
		if err := q.StoreSendKey(ctx, sqlc.StoreSendKeyParams{ApplicationID: app.ID, StaffUserID: scope.Staff.ID, KeySource: source, Key: key, NotificationID: campaignID, QueuedAt: queued.QueuedAt, DeadlineAt: queued.DeadlineAt, KeyTtlSeconds: policy.KeyTtlSeconds}); err != nil {
			return err
		}
		result = acceptanceSnapshot(campaignID, queued.QueuedAt, queued.DeadlineAt)
		return logChange(ctx, q, app, audit.ACTION_NOTIFICATION_SEND, audit.ENTITY_TYPE_NOTIFICATION, campaignID, nil, result)
	})
	if err != nil {
		return Acceptance{}, false, err
	}
	return result, replayed, nil
}

func (s *Service) ScheduleCampaign(ctx context.Context, id pgtype.UUID, campaignID int64, at time.Time) (ScheduledCampaign, error) {
	if _, err := authz.NewApplicationAuthorizer(s.q).RequireApplication(ctx, id, authz.NotificationsSchedule); err != nil {
		return ScheduledCampaign{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var result ScheduledCampaign
	err := s.write(ctx, func(q *sqlc.Queries) error {
		runtime, app, _, err := lockAcceptanceScope(ctx, q, id, authz.NotificationsSchedule)
		if err != nil {
			return err
		}
		policy, err := acceptancePolicy(ctx, q, runtime, app.ID)
		if err != nil {
			return err
		}
		campaign, err := q.LockCampaignAcceptance(ctx, sqlc.LockCampaignAcceptanceParams{ApplicationID: app.ID, CampaignID: campaignID})
		if err != nil {
			return campaignLookupError(err)
		}
		if campaign.Status != "DRAFT" {
			return errCampaignNotDraft
		}
		if policy.Managed {
			ready, err := q.CampaignAcceptanceReady(ctx, sqlc.CampaignAcceptanceReadyParams{ApplicationID: app.ID, CampaignID: campaignID, PilotCohort: policy.PilotCohort})
			if err != nil {
				return err
			}
			if !ready {
				return errAudienceNotReady
			}
		}
		row, err := q.ScheduleScopedCampaign(ctx, sqlc.ScheduleScopedCampaignParams{ApplicationID: app.ID, CampaignID: campaignID, ScheduledAt: pgtype.Timestamptz{Time: at, Valid: true}, PushTtlSeconds: policy.PushTtlSeconds, OriginEpoch: runtime.OriginEpoch, SourceEpoch: runtime.SourceEpoch})
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalidSchedule
		}
		if err != nil {
			return err
		}
		result = ScheduledCampaign{strconv.FormatInt(campaignID, 10), "SCHEDULED", row.ScheduledAt, row.DeadlineAt}
		return logChange(ctx, q, app, audit.ACTION_NOTIFICATION_SCHEDULE, audit.ENTITY_TYPE_NOTIFICATION, campaignID, nil, result)
	})
	if err != nil {
		return ScheduledCampaign{}, err
	}
	return result, nil
}

func acceptanceError(w http.ResponseWriter, err error) {
	if errors.Is(err, errSendHeld) {
		w.Header().Set("Retry-After", "5")
		response.JSON(w, http.StatusServiceUnavailable, nil, errSendHeld.Error())
		return
	}
	writeError(w, err)
}

func (h *Handler) SendCampaign(w http.ResponseWriter, r *http.Request) {
	id, campaignID, err := campaignPath(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !httprequest.RequireEmptyBody(w, r, httprequest.DefaultJSONLimit) {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) > 1 {
		writeError(w, errInvalidSendKey)
		return
	}
	key := ""
	if len(keys) == 1 {
		key = keys[0]
	}
	accepted, replayed, err := h.service.AcceptCampaign(r.Context(), id, campaignID, key)
	if err != nil {
		acceptanceError(w, err)
		return
	}
	w.Header().Set("Idempotency-Replayed", strconv.FormatBool(replayed))
	response.JSON(w, http.StatusAccepted, accepted, "campaign accepted")
}

func (h *Handler) ScheduleCampaign(w http.ResponseWriter, r *http.Request) {
	id, campaignID, err := campaignPath(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var input struct {
		ScheduledAt string `json:"scheduled_at"`
	}
	if !httprequest.DecodeStrictJSONObject(w, r, &input, httprequest.DefaultJSONLimit, "scheduled_at") {
		return
	}
	at, err := time.Parse(time.RFC3339Nano, input.ScheduledAt)
	if err != nil {
		writeError(w, errInvalidSchedule)
		return
	}
	result, err := h.service.ScheduleCampaign(r.Context(), id, campaignID, at)
	if err != nil {
		acceptanceError(w, err)
		return
	}
	response.JSON(w, http.StatusAccepted, result, "campaign scheduled")
}
