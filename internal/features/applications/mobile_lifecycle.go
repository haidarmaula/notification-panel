package applications

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/httprequest"
	"hello/internal/installation"
	"hello/pkg/response"
)

type mobileLifecycle struct{ runtime *installation.Runtime }

func RegisterMobileLifecycle(mux *http.ServeMux, runtime *installation.Runtime) {
	h := mobileLifecycle{runtime: runtime}
	mux.HandleFunc("POST /api/v1/apps/{app_id}/mobile/installations", h.serve)
	mux.HandleFunc("POST /api/v1/apps/{app_id}/mobile/installations/{installation_id}/refresh", h.serve)
	mux.HandleFunc("POST /api/v1/apps/{app_id}/mobile/installations/{installation_id}/logout", h.serve)
	mux.HandleFunc("POST /api/v1/apps/{app_id}/mobile/installations/{installation_id}/switch", h.serve)
}

type mobileLifecycleInput struct {
	Platform    string `json:"platform"`
	AddressType string `json:"address_type"`
	Address     string `json:"address"`
	Origin      string `json:"origin"`
	Proof       string `json:"proof"`
	Version     string `json:"version"`
}

type mobileLifecycleView struct {
	InstallationID string `json:"installation_id"`
	Version        string `json:"version"`
	OwnerEpoch     string `json:"owner_epoch"`
	Proof          string `json:"proof,omitempty"`
}

func mobileState(state installation.State, proof string) mobileLifecycleView {
	return mobileLifecycleView{state.PublicID.String(), strconv.FormatInt(state.Version, 10), strconv.FormatInt(state.OwnerEpoch, 10), proof}
}

func mobileBearer(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return ""
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func mobileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, installation.ErrMobileCredential), errors.Is(err, installation.ErrMobileOrigin):
		response.JSON(w, http.StatusUnauthorized, nil, "invalid mobile credentials")
	case errors.Is(err, installation.ErrNotFound):
		response.JSON(w, http.StatusNotFound, nil, "installation not found")
	case errors.Is(err, installation.ErrProjectInactive), errors.Is(err, installation.ErrVersion),
		errors.Is(err, installation.ErrAddressClaimed), errors.Is(err, installation.ErrTransition):
		response.JSON(w, http.StatusConflict, nil, err.Error())
	case errors.Is(err, installation.ErrAddress):
		badBody(w)
	default:
		response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
	}
}

func (h mobileLifecycle) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := query(r); err != nil {
		writeError(w, err)
		return
	}
	credential := mobileBearer(r)
	if credential == "" {
		mobileError(w, installation.ErrMobileCredential)
		return
	}
	value := r.PathValue("installation_id")
	var installationID pgtype.UUID
	enroll := value == ""
	if !enroll && (!uuidPattern.MatchString(value) || installationID.Scan(value) != nil) {
		badBody(w)
		return
	}
	operation := installation.Refresh
	fields := []string{"proof", "version", "platform", "address_type", "address"}
	switch {
	case enroll:
		fields = []string{"platform", "address_type", "address", "origin"}
	case strings.HasSuffix(r.Pattern, "/logout"):
		operation = installation.Logout
		fields = []string{"proof", "version"}
	case strings.HasSuffix(r.Pattern, "/switch"):
		operation = installation.SwitchOwner
	}
	var input mobileLifecycleInput
	if !httprequest.DecodeStrictJSONObject(w, r, &input, httprequest.DefaultJSONLimit, fields...) {
		return
	}
	var route installation.Route
	if operation != installation.Logout {
		var ok bool
		route, ok = h.runtime.Route(id, input.Platform, input.AddressType)
		if !ok || !installation.ValidAddress(input.Address) {
			badBody(w)
			return
		}
	}
	if enroll {
		actor, err := h.runtime.Authority.AuthenticateEnrollment(r.Context(), id, credential, input.Origin)
		if err != nil {
			mobileError(w, err)
			return
		}
		result, err := h.runtime.Lifecycle.Enroll(r.Context(), actor, route, input.Address)
		if err != nil {
			mobileError(w, err)
			return
		}
		response.JSON(w, http.StatusCreated, mobileState(result.State, result.Proof), "success")
		return
	}
	version, ok := campaignReferenceID(input.Version)
	if !ok {
		badBody(w)
		return
	}
	actor, err := h.runtime.Authority.Authenticate(r.Context(), id, credential)
	if err != nil {
		mobileError(w, err)
		return
	}
	state, err := h.runtime.Lifecycle.Apply(r.Context(), actor, installation.Change{InstallationID: installationID,
		Proof: input.Proof, ExpectedVersion: version.Int64, Operation: operation, Route: route, Address: input.Address})
	if err != nil {
		mobileError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, mobileState(state, ""), "success")
}
