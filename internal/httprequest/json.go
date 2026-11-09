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

// DecodeJSONWithMediaType requires application/json with an optional UTF-8
// charset and identity content encoding before applying DecodeJSON's body
// budget and decoding behavior. It writes 415 and returns false otherwise.
func DecodeJSONWithMediaType(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	if !jsonMediaType(r) {
		response.JSON(w, http.StatusUnsupportedMediaType, nil, "unsupported media type")
		return false
	}
	return DecodeJSON(w, r, dst, limit)
}

// DecodeJSON reads the entire bounded body before decoding exactly one JSON
// value. It writes a safe 413/400 and returns false on failure; callers must
// return before business work. Unknown fields retain encoding/json semantics.
// Route authentication/authorization remains the caller's responsibility.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	data, ok := readBody(w, r, limit)
	if !ok {
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

// readBody bounds the complete input before any parser can accept a prefix.
func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	if limit <= 0 {
		panic("httprequest: positive JSON body limit required")
	}
	if r.Body == nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return nil, false
	}
	body := http.MaxBytesReader(w, r.Body, limit)
	defer body.Close()
	if r.ContentLength > limit {
		response.JSON(w, http.StatusRequestEntityTooLarge, nil, "request body too large")
		return nil, false
	}
	data, err := io.ReadAll(body)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		response.JSON(w, http.StatusRequestEntityTooLarge, nil, "request body too large")
		return nil, false
	}
	if err != nil {
		response.JSON(w, http.StatusBadRequest, nil, "invalid request body")
		return nil, false
	}
	return data, true
}
