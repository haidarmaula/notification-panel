// Package httprequest bounds and decodes incoming HTTP JSON without exposing
// request content or parser diagnostics in error responses.
package httprequest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"hello/pkg/response"
)

const (
	// DefaultJSONLimit bounds small account/device/segment JSON requests in bytes.
	DefaultJSONLimit int64 = 64 << 10
	// CampaignJSONLimit permits bounded individual targeting lists in campaigns.
	// It is not a provider notification payload limit.
	CampaignJSONLimit int64 = 1 << 20
)

// DecodeJSON reads the entire bounded body before decoding exactly one JSON
// value. It writes a safe 413/400 and returns false on failure; callers must
// return before business work. Unknown fields retain encoding/json semantics.
// Route authentication/authorization remains the caller's responsibility.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	if limit <= 0 {
		panic("httprequest: positive JSON body limit required")
	}
	if r.Body == nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return false
	}
	body := http.MaxBytesReader(w, r.Body, limit)
	defer body.Close()
	if r.ContentLength > limit {
		response.JSON(w, http.StatusRequestEntityTooLarge, nil, "request body too large")
		return false
	}
	data, err := io.ReadAll(body)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		response.JSON(w, http.StatusRequestEntityTooLarge, nil, "request body too large")
		return false
	}
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return false
	}
	// Unmarshal consumes one complete value, rejecting trailing data before a
	// service can mutate state. Whitespace within the byte budget is permitted.
	if err := json.Unmarshal(data, dst); err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return false
	}
	return true
}
