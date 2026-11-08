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

// JSON writes the canonical response envelope. It encodes the payload before
// committing headers so an invalid payload can return a generic JSON 500.
// A transport failure after commit aborts the response with http.ErrAbortHandler.
func JSON(w http.ResponseWriter, status int, data any, message string) {
	header := w.Header()
	header.Set("Content-Type", "application/json")
	if status == http.StatusNoContent || status == http.StatusNotModified || status/100 == 1 {
		w.WriteHeader(status)
		return
	}

	body, err := json.Marshal(APIResponse{
		Success: status < 400,
		Message: message,
		Data:    data,
	})
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"success":false,"message":"internal server error"}`)
		header.Del("Content-Length")
		header.Del("ETag")
		header.Del("Location")
		header.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	body = append(body, '\n')
	if n, err := w.Write(body); err != nil || n != len(body) {
		panic(http.ErrAbortHandler)
	}
}
