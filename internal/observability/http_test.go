package observability

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"hello/pkg/response"
)

func TestAccessRecordsWireOutcome(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		require.True(t, ok)
		_, ok = w.(http.Hijacker)
		require.False(t, ok)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "done")
		f.Flush()
	})
	req := httptest.NewRequest("GET", "/api/items/private-subject?auth=private-query", nil)
	req.Header.Set("X-Request-ID", "private-id")
	req.Header.Set("Authorization", "private-bearer")
	var event Request
	w := httptest.NewRecorder()
	Access(mux, func(r Request) { event = r }).ServeHTTP(w, req)
	require.Equal(t, 202, event.Status)
	require.EqualValues(t, 4, event.Bytes)
	require.Equal(t, "/api/items/{id}", event.Route)
	require.Equal(t, "complete", event.Outcome)
	require.NotEmpty(t, event.ID)
	require.Equal(t, event.ID, w.Result().Header.Get("X-Request-ID"))
	require.NotContains(t, fmt.Sprint(event), "private")
	require.Positive(t, event.Duration)
}

func TestAccessPanic(t *testing.T) {
	for _, tc := range []struct {
		name          string
		commit, abort bool
		want          int
		outcome       string
	}{
		{"before", false, false, 500, "panic"}, {"after", true, false, 201, "panic"}, {"explicit_abort", false, true, 0, "aborted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var event Request
			w := httptest.NewRecorder()
			h := Access(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Set-Cookie", "sensitive")
				if tc.commit {
					w.WriteHeader(201)
					_, _ = io.WriteString(w, "prefix")
				}
				if tc.abort {
					panic(http.ErrAbortHandler)
				}
				panic("sensitive")
			}), func(r Request) { event = r })
			run := func() { h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil)) }
			if tc.commit || tc.abort {
				require.PanicsWithValue(t, http.ErrAbortHandler, run)
			} else {
				run()
			}
			require.Equal(t, tc.want, event.Status)
			require.Equal(t, tc.outcome, event.Outcome)
			if tc.commit {
				require.Equal(t, "prefix", w.Body.String())
			}
			if tc.want == 500 {
				require.JSONEq(t, `{"success":false,"message":"internal server error"}`, w.Body.String())
				require.Empty(t, w.Header().Get("Content-Encoding"))
				require.Empty(t, w.Header().Get("Set-Cookie"))
				require.Equal(t, event.ID, w.Header().Get("X-Request-ID"))
			}
		})
	}
}

type disconnectedWriter struct {
	*httptest.ResponseRecorder
	writes int
}

func (w *disconnectedWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, io.ErrClosedPipe
}

func TestAccessRecordsFailedJSONWrite(t *testing.T) {
	for _, tc := range []struct {
		name, outcome string
		status        int
		handler       http.HandlerFunc
	}{
		{"ordinary", "aborted", 201, func(w http.ResponseWriter, _ *http.Request) {
			response.JSON(w, 201, nil, "created")
		}},
		{"panic recovery", "panic", 500, func(http.ResponseWriter, *http.Request) {
			panic("private panic")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []Request
			w := &disconnectedWriter{ResponseRecorder: httptest.NewRecorder()}
			handler := Access(tc.handler, func(event Request) { events = append(events, event) })
			require.PanicsWithValue(t, http.ErrAbortHandler, func() {
				handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			})
			require.Equal(t, 1, w.writes)
			require.Equal(t, tc.status, w.Code)
			require.Len(t, events, 1)
			require.Equal(t, tc.status, events[0].Status)
			require.Equal(t, tc.outcome, events[0].Outcome)
			require.Zero(t, events[0].Bytes)
		})
	}
}

type richWriter struct {
	header http.Header
	bytes.Buffer
}

func (w *richWriter) Header() http.Header                          { return w.header }
func (w *richWriter) WriteHeader(int)                              {}
func (w *richWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, net.ErrClosed }

func TestAccessReaderFromAndInformationalResponse(t *testing.T) {
	var event Request
	w := &richWriter{header: http.Header{}}
	Access(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, err := w.(http.Hijacker).Hijack()
		require.ErrorIs(t, err, net.ErrClosed)
		w.WriteHeader(103)
		w.WriteHeader(201)
		n, err := w.(io.ReaderFrom).ReadFrom(strings.NewReader("body"))
		require.NoError(t, err)
		require.EqualValues(t, 4, n)
	}), func(r Request) { event = r }).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, 201, event.Status)
	require.EqualValues(t, 4, event.Bytes)
}

func TestUnknownPathsStayBounded(t *testing.T) {
	state := New()
	handler := Access(http.NewServeMux(), state.Observe)
	for i := range 1024 {
		url := fmt.Sprintf("/api/tokens/private-%d", i)
		req := httptest.NewRequest("ARBITRARY", url, nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	w := httptest.NewRecorder()
	state.handlers().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	body := w.Body.String()
	require.NotContains(t, body, "private-")
	require.Contains(t, body, `hello_http_requests_total{method="other",outcome="complete",route="unmatched",slo_outcome="excluded",status_class="4xx"} 1024`)
	require.Contains(t, body, "hello_telemetry_dropped_total 768")
	var logs bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Millisecond)
	defer cancel()
	state.logRequests(ctx, slog.New(slog.NewJSONHandler(&logs, nil)))
	require.Contains(t, logs.String(), `"route":"unmatched"`)
	require.NotContains(t, logs.String(), "private-")
}
