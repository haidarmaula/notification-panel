package applications

import (
	"errors"
	"fmt"
	"hello/internal/authz"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistrySafeErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{ErrInvalidID, 400}, {ErrInvalidStaffID, 400}, {ErrInvalidKey, 400}, {ErrInvalidName, 400}, {ErrInvalidPagination, 400}, {ErrInvalidPrecondition, 400}, {errQuery, 400},
		{authz.ErrUnauthenticated, 401}, {authz.ErrForbidden, 403}, {authz.ErrApplicationNotFound, 404}, {ErrStaffNotFound, 404}, {ErrGrantNotFound, 404},
		{ErrKeyExists, 409}, {ErrStaffInactive, 409}, {authz.ErrApplicationNotActive, 409}, {ErrPreconditionFailed, 412}, {ErrPreconditionRequired, 428}, {errors.New("private database detail"), 500},
	} {
		w := httptest.NewRecorder()
		writeError(w, fmt.Errorf("sensitive detail: %w", tc.err))
		if w.Code != tc.status || strings.Contains(w.Body.String(), "detail") {
			t.Fatalf("unsafe/wrong response: %d %s", w.Code, w.Body)
		}
	}
}

func TestRegistryConditions(t *testing.T) {
	for _, tc := range []struct {
		match, none []string
		create      bool
		err         error
	}{
		{nil, nil, false, ErrPreconditionRequired}, {[]string{""}, nil, false, ErrInvalidPrecondition}, {[]string{`W/"x"`}, nil, false, ErrInvalidPrecondition},
		{[]string{`"x", "y"`}, nil, false, ErrInvalidPrecondition}, {[]string{`*`}, nil, false, ErrInvalidPrecondition}, {[]string{`"x"`, `"x"`}, nil, false, ErrInvalidPrecondition},
		{[]string{`"x"`}, []string{"*"}, true, ErrInvalidPrecondition}, {nil, []string{"*"}, false, ErrInvalidPrecondition}, {nil, []string{"*"}, true, nil},
		{[]string{" \t\"x\"\t "}, nil, false, nil},
	} {
		r := httptest.NewRequest("PUT", "/", nil)
		r.Header["If-Match"] = tc.match
		r.Header["If-None-Match"] = tc.none
		_, err := conditions(r, tc.create)
		if !errors.Is(err, tc.err) {
			t.Fatalf("got %v want %v", err, tc.err)
		}
	}
}
