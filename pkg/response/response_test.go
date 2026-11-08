package response

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJSONPreservesResponseEnvelope(t *testing.T) {
	w := httptest.NewRecorder()
	JSON(w, http.StatusCreated, map[string]string{"id": "17"}, "created")
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "application/json", w.Result().Header.Get("Content-Type"))
	require.Equal(t, "{\"success\":true,\"message\":\"created\",\"data\":{\"id\":\"17\"}}\n", w.Body.String())
}

func TestJSONMarshalFailureCommitsSafe500(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("ETag", "\"new-version\"")
	w.Header().Set("Location", "/api/private-id")
	w.Header().Set("Content-Length", "1")
	JSON(w, http.StatusCreated, make(chan string), "private message")
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, "application/json", w.Result().Header.Get("Content-Type"))
	require.Equal(t, "no-store", w.Result().Header.Get("Cache-Control"))
	for _, name := range []string{"ETag", "Location", "Content-Length"} {
		require.Empty(t, w.Result().Header.Get(name))
	}
	require.JSONEq(t, `{"success":false,"message":"internal server error"}`, w.Body.String())
}

func TestJSONSkipsBodyForNoContent(t *testing.T) {
	w := httptest.NewRecorder()
	JSON(w, http.StatusNoContent, nil, "")
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Empty(t, w.Body.String())
}
