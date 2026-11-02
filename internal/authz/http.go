package authz

import (
	"errors"
	"hello/pkg/response"
	"net/http"
)

// WriteError handles only authorization denials. Unknown errors remain the
// feature handler's responsibility and must use its generic 500 branch.
func WriteError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		response.JSON(w, http.StatusUnauthorized, nil, "authentication required")
	case errors.Is(err, ErrForbidden):
		response.JSON(w, http.StatusForbidden, nil, "insufficient permissions")
	default:
		return false
	}
	return true
}

// Guard wraps an authenticated route; services must independently Require again.
func Guard(checker Checker, permission Permission, next http.HandlerFunc) http.HandlerFunc {
	if checker == nil {
		panic("authz: checker is required")
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := checker.Require(r.Context(), permission); err != nil {
			if !WriteError(w, err) {
				response.JSON(w, http.StatusInternalServerError, nil, "internal server error")
			}
			return
		}
		next(w, r)
	}
}
