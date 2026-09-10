// Package response provides helpers for writing standardized JSON HTTP
// responses from the Notification Panel API server.
//
// Every handler in the codebase emits responses through this package so that
// clients can rely on a single, stable envelope shape:
//
//	{
//	  "success": true,
//	  "message": "optional human-readable note",
//	  "data":    { ... } // omitted when nil
//	}
//
// The envelope is deliberately minimal: transport-level concerns (status
// code, Content-Type) stay in the HTTP layer, while application-level
// concerns (the payload and an optional message) live in the JSON body. This
// keeps client-side decoding uniform and avoids duplicating status-code
// semantics inside the body.
//
// The package is stateless and therefore safe for concurrent use from any
// number of handler goroutines.
package response

import (
	"encoding/json"
	"net/http"
)

// APIResponse is the canonical JSON envelope returned by all HTTP handlers in
// the Notification Panel API.
//
// Fields are tagged with omitempty where the absence of a value is
// meaningful:
//
//   - Message is omitted when empty, so clients can distinguish "no message"
//     from "empty message".
//   - Data is omitted when nil, keeping error responses terse and avoiding
//     the awkward `"data": null` on endpoints that have nothing to return.
//
// Success is never omitted — it is always serialized as a boolean so that
// clients can branch on a single, always-present field rather than inferring
// success from the HTTP status alone.
type APIResponse struct {
	// Success indicates whether the request was handled successfully. It is
	// derived from the HTTP status code by JSON (status < 400) and is not
	// set independently by callers.
	Success bool `json:"success"`

	// Message is an optional human-readable string accompanying the response.
	// It is omitted from the JSON body when empty. Callers typically populate
	// it with a short success note or a domain error description; it is not
	// intended to carry machine-parseable error codes.
	Message string `json:"message,omitempty"`

	// Data is the response payload. It is omitted from the JSON body when
	// nil. Any JSON-serializable value is accepted; typed structs, slices,
	// and maps are the expected inputs.
	Data any `json:"data,omitempty"`
}

// JSON writes an APIResponse envelope to w with the given HTTP status code,
// payload, and message.
//
// # Behavior
//
// The function sets the Content-Type header to application/json, writes the
// status code, and encodes an APIResponse whose Success field is computed as
// status < 400. Callers therefore control the success flag indirectly by
// choosing the status code — passing a 2xx or 3xx status yields success=true,
// while any 4xx or 5xx status yields success=false.
//
// # Parameters
//
//   - w: the response writer. Must not be nil. The function must be called
//     before any other write to w; calling it after headers have already been
//     flushed will result in a "superfluous WriteHeader call" warning from
//     net/http and the status code will not take effect.
//   - status: the HTTP status code to send. Any value accepted by
//     http.ResponseWriter.WriteHeader is valid; the conventional inputs are
//     the http.Status* constants.
//   - data: the response payload. May be nil, in which case the "data" field
//     is omitted from the JSON body. Any value accepted by encoding/json may
//     be passed.
//   - message: an optional human-readable note. Pass an empty string to omit
//     the "message" field entirely.
//
// # Error handling
//
// The function does not return an error. If json.Encoder.Encode fails (for
// example, because data contains a channel, function, or cyclic reference),
// the failure is currently ignored: the status code and headers have already
// been sent, so the client will receive a truncated body with no indication
// that encoding failed. Callers whose payloads could plausibly fail to encode
// should validate them before invoking JSON, or write directly to w if they
// need to surface the error.
//
// # Concurrency
//
// JSON is safe for concurrent use provided each invocation targets a distinct
// http.ResponseWriter. As with all net/http handlers, a single
// http.ResponseWriter must not be written to from multiple goroutines
// simultaneously; that constraint is inherited from the standard library and
// is not relaxed by this function.
func JSON(w http.ResponseWriter, status int, data any, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	json.NewEncoder(w).Encode(APIResponse{
		Success: status < 400,
		Message: message,
		Data:    data,
	})
}
