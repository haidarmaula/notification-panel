package applications

import (
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/dispatch"
	"hello/internal/httprequest"
	"hello/internal/installation"
	"hello/internal/receipt"
	"hello/pkg/response"
)

// RegisterMobileReceipts enables the endpoint for applications with explicit
// receipt keys, TLS policy and rate limits in the loaded mobile configuration.
func RegisterMobileReceipts(mux *http.ServeMux, runtime *installation.Runtime, db Transactor) error {
	type projectReceipt struct {
		policy installation.ReceiptPolicy
		store  *dispatch.Observations
	}
	projects := make(map[pgtype.UUID]projectReceipt)
	for id, p := range runtime.ReceiptPolicies() {
		store, err := dispatch.NewObservations(db, p.Verifier, p.InstallationPerMinute)
		if err != nil {
			return err
		}
		projects[id] = projectReceipt{p, store}
	}
	if len(projects) == 0 {
		return nil
	}
	mux.HandleFunc("POST /api/v1/apps/{app_id}/mobile/notifications/{campaign_id}/receipt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		app, campaign, err := campaignPath(r)
		if err != nil {
			writeError(w, err)
			return
		}
		project, ok := projects[app]
		if !ok {
			writeReceiptError(w, dispatch.ErrObservationNotFound)
			return
		}
		ip, ok := receiptClient(r, project.policy)
		if !ok {
			response.JSON(w, http.StatusForbidden, nil, "secure transport required")
			return
		}
		if ok, delay := project.policy.IP.Allow(ip.String(), time.Now()); !ok {
			receiptLimited(w, delay)
			return
		}
		session, err := runtime.Authority.AuthenticateSession(r.Context(), app, mobileBearer(r))
		if err != nil {
			mobileError(w, err)
			return
		}
		if ok, delay := project.policy.Principal.Allow(strconv.FormatInt(session.Principal().UserID, 10), time.Now()); !ok {
			receiptLimited(w, delay)
			return
		}
		var input struct {
			Delivery   string     `json:"delivery_id"`
			ID         string     `json:"client_event_id"`
			Event      string     `json:"event"`
			Token      string     `json:"receipt_token"`
			ObservedAt *time.Time `json:"observed_at"`
		}
		if !httprequest.DecodeStrictJSONObject(w, r, &input, 4096, "delivery_id", "client_event_id", "event", "receipt_token", "observed_at") {
			return
		}
		report := dispatch.Observation{CampaignID: campaign, Event: input.Event, Token: input.Token, ObservedAt: input.ObservedAt}
		if report.DeliveryID.Scan(input.Delivery) != nil || report.DeliveryID.String() != input.Delivery ||
			report.ClientEventID.Scan(input.ID) != nil || report.ClientEventID.String() != input.ID {
			badBody(w)
			return
		}
		if err = project.store.Record(r.Context(), app, session, report); err != nil {
			writeReceiptError(w, err)
			return
		}
		response.JSON(w, http.StatusOK, nil, "receipt acknowledged")
	})
	return nil
}

func receiptClient(r *http.Request, p installation.ReceiptPolicy) (netip.Addr, bool) {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	ip := peer.Addr().Unmap()
	if p.TrustsProxy(ip) {
		proto, addresses := r.Header.Values("X-Forwarded-Proto"), r.Header.Values("X-Forwarded-For")
		if len(proto) != 1 || proto[0] != "https" || len(addresses) != 1 {
			return netip.Addr{}, false
		}
		client, err := netip.ParseAddr(addresses[0])
		return client.Unmap(), err == nil && client.Zone() == "" && !client.IsUnspecified() && !client.IsMulticast()
	}
	return ip, r.TLS != nil
}

func receiptLimited(w http.ResponseWriter, delay int) {
	w.Header().Set("Retry-After", strconv.Itoa(delay))
	response.JSON(w, http.StatusTooManyRequests, nil, "receipt rate limit exceeded")
}

func writeReceiptError(w http.ResponseWriter, err error) {
	var limited *dispatch.ObservationRateLimit
	switch {
	case errors.Is(err, dispatch.ErrObservationInput):
		badBody(w)
	case errors.Is(err, dispatch.ErrObservationNotFound):
		response.JSON(w, 404, nil, "delivery not found")
	case errors.Is(err, dispatch.ErrObservationConflict):
		response.JSON(w, 409, nil, "client event ID reused")
	case errors.Is(err, receipt.ErrExpired):
		response.JSON(w, 410, nil, "receipt capability expired")
	case errors.Is(err, dispatch.ErrExecutionHeld):
		response.JSON(w, 503, nil, "receipt temporarily unavailable")
	case errors.As(err, &limited):
		receiptLimited(w, limited.RetryAfter)
	default:
		mobileError(w, err)
	}
}
