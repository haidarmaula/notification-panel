package segments

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"hello/internal/middleware"
	"hello/internal/token"
)

// parseInt64FromPath extracts and parses int64 from URL path parameter.
//
// # Behaviour
//
// The function reads the path parameter named by key via r.PathValue,
// which is populated only for routes registered with the Go 1.22
// method-and-wildcard ServeMux syntax. An empty value or a non-numeric
// string both produce a non-nil error; the caller is expected to map any
// error to a 400. The returned int64 on error is 0, but callers should
// treat the value as unspecified when the error is non-nil.
//
// The function does not range-check the parsed value; a caller-supplied
// negative number is accepted and returned as-is. It also does not
// distinguish "the parameter was missing" from "the parameter was not a
// number" — both produce an error whose message is "missing id", which is
// slightly misleading for the second case.
//
// # Duplication
//
// This function is identical to notifications.parseInt64FromPath, and
// near-identical to the versions in mobile and users if those packages
// have their own. It is redeclared here rather than shared because it is
// unexported in each package and the packages do not import each other.
// A future refactor that extracts these into a shared request-parsing
// helper package would collapse the copies; the current code duplicates
// them.
func parseInt64FromPath(r *http.Request, key string) (int64, error) {
	raw := r.PathValue(key)
	if raw == "" {
		return 0, errors.New("missing id")
	}
	return strconv.ParseInt(raw, 10, 64)
}

// getPaginationParams extracts page and limit from query parameters.
//
// # Defaults and clamping
//
// The function applies a default of page=1 and limit=10 when a parameter
// is absent, and silently clamps out-of-range values rather than rejecting
// them:
//
//   - A page value that fails to parse, or that parses to zero or a
//     negative number, is replaced by 1. The client receives the default
//     page without any indication that its request was invalid.
//   - A limit value that fails to parse, or that parses to zero, a
//     negative number, or a value greater than 100, is replaced by 10. A
//     client requesting limit=5000 receives a page of 10 items with no
//     indication that its limit was rejected.
//
// Clients should treat the values echoed in the response's Pagination
// object as authoritative, not the values they sent.
//
// # Duplication
//
// This function is byte-for-byte identical to
// notifications.getPaginationParams, with the same defaults and the same
// silent-clamping policy. It is redeclared here for the same reason as
// parseInt64FromPath: the packages are independent, and the helpers are
// unexported. A shared request-parsing helper package would collapse
// them.
func getPaginationParams(r *http.Request) (page, limit int32) {
	page = 1
	limit = 10
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.ParseInt(p, 10, 32); err == nil && v > 0 {
			page = int32(v)
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.ParseInt(l, 10, 32); err == nil && v > 0 && v <= 100 {
			limit = int32(v)
		}
	}
	return
}

// getStaffIDFromContext retrieves staff ID from context.
//
// It reads middleware.UserContextKey and type-asserts the value to
// *token.AccessClaims, returning the StaffID field and a boolean
// indicating whether a valid claim was found. An empty claims value, a
// wrong type at the key, or a missing key all yield (0, false); the
// handler treats that result as an unauthenticated request and returns
// 401.
//
// # Duplication — fourth copy in the codebase
//
// This function is semantically identical to audit.GetStaffID,
// notifications.getStaffIDFromContext, and
// profile.GetStaffIDFromContext. All four read the same context key,
// perform the same type assertion, and return the same two values. The
// duplication is structural — each feature package would need to import
// audit or middleware to reuse the helper, and the current package
// layout discourages that — but it means a change to how staff identity
// is represented in the context must be applied in four places.
//
// A future refactor that moves the extraction into a small,
// dependency-free helper package would collapse the four copies into one
// and eliminate the maintenance hazard. Until then, this comment
// documents the state so that any such change is applied consistently.
//
// # Return-value semantics
//
// The returned ID is the internal staff_users.id, not any external
// identifier. A returned value of 0 with ok == true is theoretically
// possible (a token with a StaffID of 0) but is treated as
// unauthenticated by the segment handler, which checks both ok and
// staffID != 0.
func getStaffIDFromContext(ctx context.Context) (int64, bool) {
	claims, ok := ctx.Value(middleware.UserContextKey).(*token.AccessClaims)
	if !ok || claims == nil {
		return 0, false
	}
	return claims.StaffID, true
}
