package applications

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/authz"
	"hello/internal/database/sqlc"
	"hello/internal/httprequest"
	"hello/pkg/response"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler {
	if service == nil {
		panic("applications: service is required")
	}
	return &Handler{service: service}
}

type applicationDTO struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Revision  string    `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type grantDTO struct {
	ApplicationID string    `json:"application_id"`
	StaffID       string    `json:"staff_id"`
	IsActive      bool      `json:"is_active"`
	Revision      string    `json:"revision"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
type listDTO[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
	Page  int32 `json:"page"`
	Limit int32 `json:"limit"`
}

func appDTO(a sqlc.Application) applicationDTO {
	return applicationDTO{a.PublicID.String(), a.Key, a.Name, a.Status, strconv.FormatInt(a.Revision, 10), a.CreatedAt.Time.UTC(), a.UpdatedAt.Time.UTC()}
}
func grantView(id pgtype.UUID, g sqlc.ApplicationStaffGrant) grantDTO {
	return grantDTO{id.String(), strconv.FormatInt(g.StaffID, 10), g.IsActive, strconv.FormatInt(g.Revision, 10), g.CreatedAt.Time.UTC(), g.UpdatedAt.Time.UTC()}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var staffIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
var errQuery = errors.New("invalid query parameters")

func pathID(r *http.Request) (pgtype.UUID, error) {
	var id pgtype.UUID
	value := r.PathValue("app_id")
	if !uuidPattern.MatchString(value) {
		return id, ErrInvalidID
	}
	if err := id.Scan(value); err != nil {
		return id, ErrInvalidID
	}
	return id, nil
}
func pathStaff(r *http.Request) (int64, error) {
	value := r.PathValue("staff_id")
	if !staffIDPattern.MatchString(value) {
		return 0, ErrInvalidStaffID
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrInvalidStaffID
	}
	return id, nil
}
func query(r *http.Request, allowed ...string) (url.Values, error) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, errQuery
	}
	for k, v := range values {
		known := false
		for _, name := range allowed {
			if k == name {
				known = true
				break
			}
		}
		if !known || len(v) != 1 {
			return nil, errQuery
		}
	}
	return values, nil
}
func pagination(v url.Values) (int32, int32, error) {
	page, limit := int64(1), int64(20)
	var err error
	if v.Has("page") {
		page, err = strconv.ParseInt(v.Get("page"), 10, 32)
		if err != nil {
			return 0, 0, ErrInvalidPagination
		}
	}
	if v.Has("limit") {
		limit, err = strconv.ParseInt(v.Get("limit"), 10, 32)
		if err != nil {
			return 0, 0, ErrInvalidPagination
		}
	}
	if _, err := pageOffset(int32(page), int32(limit)); err != nil {
		return 0, 0, err
	}
	return int32(page), int32(limit), nil
}
func conditions(r *http.Request, create bool) (Preconditions, error) {
	match, none := r.Header.Values("If-Match"), r.Header.Values("If-None-Match")
	if len(match) > 1 || len(none) > 1 {
		return Preconditions{}, ErrInvalidPrecondition
	}
	c := Preconditions{}
	if len(match) == 1 {
		c.IfMatch = strings.Trim(match[0], " \t")
		if c.IfMatch == "" {
			return c, ErrInvalidPrecondition
		}
	}
	if len(none) == 1 {
		c.IfNoneMatch = strings.Trim(none[0], " \t")
		if c.IfNoneMatch == "" {
			return c, ErrInvalidPrecondition
		}
	}
	return c, c.validate(create)
}
func writeError(w http.ResponseWriter, err error) {
	if authz.WriteError(w, err) {
		return
	}
	cases := []struct {
		err    error
		status int
	}{
		{ErrInvalidID, 400}, {errInvalidSegmentID, 400}, {errSegmentNotFound, 404}, {errCampaignReference, 404}, {errCampaignNotFound, 404}, {errTemplateNotFound, 404}, {errInvalidTemplateID, 400}, {errInvalidCampaignID, 400}, {errCampaignNotDraft, 409}, {errCampaignProtected, 409}, {errSegmentMemberMissing, 404}, {errSegmentNameTaken, 409}, {errSegmentReferenced, 409}, {errSegmentMemberExists, 409}, {errInvalidUserID, 400}, {errUserNotFound, 404}, {ErrInvalidStaffID, 400}, {ErrInvalidKey, 400}, {ErrInvalidName, 400}, {ErrInvalidPagination, 400}, {ErrInvalidPrecondition, 400}, {errQuery, 400},
		{authz.ErrApplicationNotFound, 404}, {ErrStaffNotFound, 404}, {ErrGrantNotFound, 404},
		{ErrKeyExists, 409}, {ErrStaffInactive, 409}, {authz.ErrApplicationNotActive, 409}, {ErrPreconditionFailed, 412}, {ErrPreconditionRequired, 428},
	}
	for _, c := range cases {
		if errors.Is(err, c.err) {
			response.JSON(w, c.status, nil, c.err.Error())
			return
		}
	}
	response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
}
func badBody(w http.ResponseWriter) { response.JSON(w, 400, nil, "invalid request body") }

// ListGranted and GetGranted expose metadata only after current membership checks.
func (h *Handler) ListGranted(w http.ResponseWriter, r *http.Request) { h.list(w, r, false) }
func (h *Handler) ListManaged(w http.ResponseWriter, r *http.Request) { h.list(w, r, true) }
func (h *Handler) list(w http.ResponseWriter, r *http.Request, managed bool) {
	allowed := []string{"page", "limit"}
	if managed {
		allowed = append(allowed, "key")
	}
	v, err := query(r, allowed...)
	if err != nil {
		writeError(w, err)
		return
	}
	page, limit, err := pagination(v)
	if err != nil {
		writeError(w, err)
		return
	}
	key := v.Get("key")
	if v.Has("key") && key == "" {
		writeError(w, ErrInvalidKey)
		return
	}
	if managed {
		data, err := h.service.ListManaged(r.Context(), key, page, limit)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]applicationDTO, 0, len(data.Items))
		for _, a := range data.Items {
			items = append(items, appDTO(a))
		}
		response.JSON(w, 200, listDTO[applicationDTO]{items, data.Total, page, limit}, "success")
		return
	}
	data, err := h.service.ListGranted(r.Context(), page, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]applicationDTO, 0, len(data.Items))
	for _, a := range data.Items {
		items = append(items, appDTO(a))
	}
	response.JSON(w, 200, listDTO[applicationDTO]{items, data.Total, page, limit}, "success")
}
func (h *Handler) GetGranted(w http.ResponseWriter, r *http.Request) { h.detail(w, r, false) }
func (h *Handler) GetManaged(w http.ResponseWriter, r *http.Request) { h.detail(w, r, true) }
func (h *Handler) detail(w http.ResponseWriter, r *http.Request, managed bool) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err = query(r); err != nil {
		writeError(w, err)
		return
	}
	var app sqlc.Application
	if managed {
		app, err = h.service.GetManaged(r.Context(), id)
	} else {
		app, err = h.service.GetGranted(r.Context(), id)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("ETag", ApplicationETag(app))
	response.JSON(w, 200, appDTO(app), "success")
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if _, err := query(r); err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Key  *string `json:"key"`
		Name *string `json:"name"`
	}
	if !httprequest.DecodeStrictJSONObject(w, r, &req, httprequest.DefaultJSONLimit, "key", "name") {
		return
	}
	if req.Key == nil || req.Name == nil {
		badBody(w)
		return
	}
	if len(r.Header.Values("If-Match")) > 0 || len(r.Header.Values("If-None-Match")) > 0 {
		writeError(w, ErrInvalidPrecondition)
		return
	}
	app, err := h.service.Create(r.Context(), *req.Key, *req.Name)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("ETag", ApplicationETag(app))
	w.Header().Set("Location", "/api/v1/admin/apps/"+app.PublicID.String())
	response.JSON(w, 201, appDTO(app), "application created")
}
func (h *Handler) Rename(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err = query(r); err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Name *string `json:"name"`
	}
	if !httprequest.DecodeStrictJSONObject(w, r, &req, httprequest.DefaultJSONLimit, "name") {
		return
	}
	if req.Name == nil {
		badBody(w)
		return
	}
	c, err := conditions(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	app, err := h.service.Rename(r.Context(), id, *req.Name, c)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("ETag", ApplicationETag(app))
	response.JSON(w, 200, appDTO(app), "success")
}
func (h *Handler) ListGrants(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	v, err := query(r, "page", "limit")
	if err != nil {
		writeError(w, err)
		return
	}
	page, limit, err := pagination(v)
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := h.service.ListGrants(r.Context(), id, page, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]grantDTO, 0, len(data.Items))
	for _, g := range data.Items {
		items = append(items, grantView(id, g))
	}
	response.JSON(w, 200, listDTO[grantDTO]{items, data.Total, page, limit}, "success")
}
func (h *Handler) GetGrant(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	staff, err := pathStaff(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err = query(r); err != nil {
		writeError(w, err)
		return
	}
	g, err := h.service.GetGrant(r.Context(), id, staff)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("ETag", GrantETagForID(id, g))
	response.JSON(w, 200, grantView(id, g), "success")
}
func (h *Handler) EnableGrant(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	staff, err := pathStaff(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err = query(r); err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		IsActive *bool `json:"is_active"`
	}
	if !httprequest.DecodeStrictJSONObject(w, r, &req, httprequest.DefaultJSONLimit, "is_active") {
		return
	}
	if req.IsActive == nil || !*req.IsActive {
		badBody(w)
		return
	}
	c, err := conditions(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := h.service.EnableGrant(r.Context(), id, staff, c)
	if err != nil {
		writeError(w, err)
		return
	}
	status := 200
	if data.Created {
		status = 201
		w.Header().Set("Location", "/api/v1/admin/apps/"+id.String()+"/staff-grants/"+strconv.FormatInt(staff, 10))
	}
	w.Header().Set("ETag", GrantETagForID(id, data.Grant))
	response.JSON(w, status, grantView(id, data.Grant), "success")
}
func (h *Handler) RevokeGrant(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	staff, err := pathStaff(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err = query(r); err != nil {
		writeError(w, err)
		return
	}
	if !httprequest.RequireEmptyBody(w, r, httprequest.DefaultJSONLimit) {
		return
	}
	c, err := conditions(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	if err = h.service.RevokeGrant(r.Context(), id, staff, c); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
